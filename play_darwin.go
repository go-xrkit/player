//go:build darwin

package player

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"sync"
	"time"

	"github.com/go-images/depth"
	"github.com/go-macos/coreaudio"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/window"
	"github.com/go-xrkit/depth3d"
	"github.com/go-xrkit/xrkit/glasses"
	"github.com/go-xrkit/xrkit/pose"
	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
	"github.com/go-xrkit/xrkit/warp"
)

// black is opaque black as a little-endian RGBA word: the bytes in memory are
// R=0, G=0, B=0, A=255. It fills whatever the content does not cover.
const black uint32 = 0xff000000

// Play opens the file, puts a full-screen window on the chosen display, and
// plays until the file ends or the window closes.
//
// It must be called from the process main goroutine: the window back-end pins
// that thread for AppKit, which requires it.
func Play(cfg Config) error {
	// ORDER MATTERS HERE, and it cost a crash to learn.
	//
	// AppKit is built FIRST -- the window, and with it NSApplication -- and only
	// then is AVFoundation touched. Opening a player before NSApplication exists,
	// and pumping the main run loop to load it, leaves AppKit half-initialised:
	// -[NSApplication finishLaunching] later pops an autorelease pool holding a
	// half-built NSView and dies in -[NSView _finalize] with an unrecognised
	// selector on its backing layer. The stack names AppKit, so it reads like an
	// AppKit bug; it is an ordering mistake.
	screens, err := window.Screens()
	if err != nil {
		return fmt.Errorf("player: cannot enumerate displays: %w", err)
	}
	displays := make([]Display, len(screens))
	for i, s := range screens {
		displays[i] = Display{Name: s.Name, Width: s.Width, Height: s.Height, Primary: s.Primary, Scale: s.Scale}
	}
	chosen, err := ChooseDisplay(displays, cfg.Screen)
	if err != nil {
		return err
	}
	// Find the window.Screen the choice refers to, which is what Config.Screen
	// wants — the Display type deliberately carries no back-end handle.
	var target window.Screen
	for _, s := range screens {
		if s.Name == chosen.Name && s.Width == chosen.Width && s.Height == chosen.Height {
			target = s
			break
		}
	}

	stereoscopic, _, _ := StereoMode(chosen.Width, chosen.Height)
	if cfg.Mono {
		stereoscopic = false
	}

	cfg.logf("display   %s", chosen)
	if advice := ScalingAdvice(chosen); advice != "" {
		cfg.logf("  WARNING: %s", advice)
	}
	if stereoscopic {
		cfg.logf("  side-by-side 3D mode: one eye per half")
	} else {
		cfg.logf("  single view (the panel is not in a side-by-side mode)")
	}

	theme := toolkit.DefaultDark()
	win, err := window.Open(window.Config{
		Title: "xrplay",
		// A self-rendering root composes its own pixels at whatever size it is
		// handed, which is exactly the case NativeScale is for.
		RenderScale: window.NativeScale,
		Screen:      &target,
		Fullscreen:  true,
		Theme:       theme,
	})
	if err != nil {
		return fmt.Errorf("player: cannot open a window on %s: %w", chosen, err)
	}
	defer win.Close()

	fbW, fbH := win.Size()
	cfg.logf("framebuffer %dx%d", fbW, fbH)
	if fbW <= 0 || fbH <= 0 {
		return fmt.Errorf("player: the window reported a %dx%d framebuffer", fbW, fbH)
	}

	// AppKit exists now and its run loop is NOT yet running -- window.Run has not
	// been called -- which is the one window in which a clocked source can be
	// loaded by pumping the main run loop.
	// The decoder pads rows, and by how much is not knowable until a frame comes
	// out -- but the sampling tables bake the stride into every offset, so it has
	// to be known BEFORE they are built. openPlaying therefore hands back a
	// source that has already produced one, and that first frame is kept: opening
	// the file again to measure it would mean reading a fifteen-gigabyte
	// recording twice.
	// Where the sound goes. Glasses publish their own audio device, and with
	// nothing named the system plays to its default output -- the Mac's
	// speakers -- while the picture is in the glasses. Nothing reports that.
	audioUID := ""
	if devs, err := coreaudio.Devices(); err != nil {
		cfg.logf("sound     cannot list the audio devices (%v), so the system chooses", err)
	} else if d, ok := pickAudio(devs, chosen.Name, cfg.AudioDevice); ok {
		audioUID = d.UID
		cfg.logf("sound     %v", d)
	} else if cfg.AudioDevice != "" {
		cfg.logf("sound     no output device matches %q, so the system chooses", cfg.AudioDevice)
	} else if def, err := coreaudio.DefaultOutput(); err == nil {
		cfg.logf("sound     %q has no output of its own, so the system default %q gets it", chosen.Name, def.Name)
	}

	src, clock, first, err := openPlaying(cfg.Path, audioUID, cfg.logf)
	if err != nil {
		return fmt.Errorf("player: cannot play %s: %w", cfg.Path, err)
	}
	defer src.Close()
	info := src.Info()
	strideWords := first.StrideWords

	geom := Detect(cfg.Path, info.Width, info.Height)
	if cfg.Geometry != nil {
		geom = *cfg.Geometry
		geom.Why = "given by the caller"
	}
	cfg.logf("file      %s", cfg.Path)
	cfg.logf("  %dx%d  %.3f fps  %v", info.Width, info.Height, info.FrameRate, info.Duration.Round(time.Millisecond))
	cfg.logf("  via %s", info.Container)
	if ac, ok := src.(audioClocked); ok {
		cfg.logf("  audio: %s", ac.AudioNote())
		if err := ac.StartAudio(); err != nil {
			cfg.logf("  audio failed to start (%v); playing silently", err)
		}
	}
	cfg.logf("content   %s", geom)
	cfg.logf("  because: %s", geom.Why)

	// The conversion happens entirely HERE, between the source and the view:
	// a flat film is wrapped in something that presents side-by-side frames,
	// and everything downstream goes on believing it was handed a 3D film.
	play := src
	if cfg.Convert {
		switch {
		case !stereoscopic:
			cfg.logf("convert   asked for, but this display shows one eye; there is nothing to convert to")
		case geom.Format.Layout.Stereoscopic():
			cfg.logf("convert   asked for, but this film is already %s", geom.Format.Layout)
		default:
			conv, err := depth3d.New(depth3d.Options{
				Model:    cfg.DepthModel,
				MaxShift: cfg.disparityOrDefault(),
				Soften:   softenRadius,
				Curve:    cfg.DepthCurve,
				Log:      func(s string) { cfg.logf("  %s", s) },
			})
			if err != nil {
				return fmt.Errorf("player: cannot convert %s to 3D: %w", cfg.Path, err)
			}
			cs := convertSource(src, conv)
			defer cs.Close()
			// The first frame was already pulled, to measure the stride before
			// the sampling tables could be built. It goes through exactly the
			// same conversion, or the tables would be built for the wrong shape.
			pair, err := cs.frame(first)
			if err != nil {
				return fmt.Errorf("player: cannot convert %s to 3D: %w", cfg.Path, err)
			}
			first.Release()
			first, play = pair, cs
			info = cs.Info()
			strideWords = first.StrideWords
			geom.Format.Layout = stereo.SideBySide
			geom.Why = "a flat film converted, " + conv.Describe()
			cfg.logf("convert   flat to 3D: %s", conv.Describe())
			cfg.logf("  eyes %d pixels apart at the nearest, depth softened by %d",
				cfg.disparityOrDefault(), softenRadius)
			if c := depth.Sigmoid(cfg.DepthCurve); c != nil {
				d := depth.DisparityOf(c, depth.Options{MaxShift: cfg.disparityOrDefault()})
				cfg.logf("  depth curve %.1f: the middle of the range gains %d pixel(s), the near end loses %d",
					cfg.DepthCurve, d[160]-160*cfg.disparityOrDefault()/255/2,
					(255*cfg.disparityOrDefault()/255/2-200*cfg.disparityOrDefault()/255/2)-(d[255]-d[200]))
			}
		}
	}

	// The eye the picture is composed into, which is what both the model's
	// field of view and the screen fitting are measured against.
	eyeAspect := float64(fbW) / float64(fbH)
	if stereoscopic {
		eyeAspect = float64(fbW/2) / float64(fbH)
	}
	prof, how := identifyGlasses(chosen.Name, usbDevices())
	fovy, known := viewFOV(prof, eyeAspect)
	switch {
	case cfg.FOVyDeg > 0:
		fovy = cfg.FOVyDeg
		cfg.logf("glasses   %.0f deg vertical, as asked for", fovy)
	case known:
		cfg.logf("glasses   %s by %s: %.1f deg vertical, from %.0f deg %s (%s)",
			prof.Model, how, fovy, prof.PublishedFOV, prof.Axis, prof.Source)
	case how != glasses.NotIdentified:
		// Recognised, but not to a model with a usable figure -- a brand, or a
		// model whose maker published an angle without saying which one. The
		// catalogue refuses to guess and so does this.
		cfg.logf("glasses   %s by %s, but no usable published field of view, so %.0f deg is a framing; the picture still fills the view",
			prof.Model, how, fovy)
	default:
		cfg.logf("glasses   not identified among %d USB device(s), so %.0f deg is a framing; the picture still fills the view",
			len(usbDevices()), fovy)
	}
	geom, filled := fitToView(geom, info, eyeAspect, fovy)
	if filled {
		cfg.logf("screen    %.1f x %.1f deg, sized to fill the eye",
			geom.Projection.HSpanDeg, geom.Projection.VSpanDeg)
	}

	v := newView(fbW, fbH, stereoscopic, geom, info, strideWords, fovy)
	cfg.logf("view      %d eye(s) of %dx%d, %.1f deg vertical, %.1f%% of the view covered",
		len(v.maps), v.eyeW, v.eyeH, fovy, v.coverage*100)

	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }
	defer closeStop()

	shown := 0
	// Declared before the surface so the paint callback can snapshot the whole
	// tree; it is built below, once the bar exists.
	var overlay *toolkit.Overlay
	surface := toolkit.NewSurface(v.frame)
	if clock != nil {
		// A clocked source must be polled from the MAIN thread, and the paint
		// callback is the only place in this program that runs there once the
		// window owns the run loop. So the warp happens inside the paint: at 3 ms
		// for both eyes against a 16.6 ms budget, that is affordable, and it
		// removes the double buffer and its lock entirely -- poll and paint are
		// the same thread, serialised by construction.
		surface.Frame = func() ([]byte, int, int) {
			f, err := src.Next()
			if err != nil {
				cfg.logf("playback stopped: %v", err)
				closeStop()
			} else if f != nil {
				v.composeInto(v.front, f)
				f.Release()
				shown++
				if cfg.Snapshot != "" && shown == cfg.SnapshotAfter+1 {
					if err := v.writeSnapshot(cfg.Snapshot, overlay, theme); err != nil {
						cfg.logf("snapshot failed: %v", err)
					} else {
						cfg.logf("snapshot of frame %d written to %s", shown-1, cfg.Snapshot)
					}
				}
			}
			return v.frame()
		}
	}
	pause := newPauser()
	volume := 1.0

	// The keyboard and the on-screen buttons drive the SAME closures. Two copies
	// of what "pause" means is how they drift apart.
	togglePause := func() {
		if pause.Toggle() {
			if clock != nil {
				clock.Pause()
			}
			cfg.logf("paused")
		} else {
			if clock != nil {
				clock.Play()
			}
			cfg.logf("resumed")
		}
	}
	seekTo := func(at time.Duration) {
		if clock == nil {
			cfg.logf("seeking needs a clocked source; this file is decoded by pulling")
			return
		}
		if at < 0 {
			at = 0
		}
		if d := info.Duration; d > 0 && at > d {
			at = d
		}
		if err := clock.Seek(at); err != nil {
			cfg.logf("seek failed: %v", err)
		} else {
			cfg.logf("at %v", at.Round(time.Second))
		}
	}
	seekBy := func(d time.Duration) {
		if clock == nil {
			cfg.logf("seeking needs a clocked source; this file is decoded by pulling")
			return
		}
		seekTo(clock.CurrentTime() + d)
	}
	setVolume := func(v float64) {
		if clock == nil {
			cfg.logf("this file is played without sound")
			return
		}
		volume = clampVolume(v)
		clock.SetVolume(volume)
		cfg.logf("volume %.0f%%", volume*100)
	}

	// noteActivity is assigned once the overlay exists; until then a key press
	// simply does its job without waking a bar that is not built yet.
	noteActivity := func() {}
	// notePointer is likewise assigned once there is a layer to move.
	notePointer := func(int, int) {}

	surface.OnInput = func(ev toolkit.Event) {
		switch ev.Kind {
		case toolkit.EventMouseMove:
			// Moving the pointer is what brings the controls up, exactly as every
			// other player does it -- and it is also the only way this program
			// learns where the pointer IS, which is what lets it draw one.
			notePointer(ev.X, ev.Y)
			noteActivity()
			return
		case toolkit.EventKeyDown:
		default:
			return
		}
		noteActivity()
		switch KeyAction(ev.Code) {
		case ActionQuit:
			closeStop()
		case ActionTogglePause:
			togglePause()
		case ActionSeekBack:
			seekBy(-SeekStep)
		case ActionSeekForward:
			seekBy(SeekStep)
		case ActionVolumeUp:
			setVolume(volume + VolumeStep)
		case ActionVolumeDown:
			setVolume(volume - VolumeStep)
		}
	}

	repaint := func() {}
	if r, ok := win.(window.Repainter); ok {
		repaint = r.Repaint
	} else {
		cfg.logf("  NOTE: this back-end cannot be repainted from another goroutine; playback will not animate")
	}

	if cfg.For > 0 {
		go func() {
			select {
			case <-time.After(cfg.For):
				cfg.logf("stopping after %v", cfg.For)
				closeStop()
			case <-stop:
			}
		}()
	}

	// --- the transport overlay ----------------------------------------------
	//
	// Content is the video; the controls are a layer above it. Events route
	// top-down, so a click on a button reaches the button and everything else
	// falls through to the video.
	var bar *controlBar
	acts := barActions{TogglePause: togglePause}
	if clock != nil {
		acts.Restart = func() { seekTo(0) }
		acts.Back = func() { seekBy(-SeekStep) }
		acts.Forward = func() { seekBy(SeekStep) }
		acts.ToggleMute = func() {
			if volume > 0 {
				setVolume(0)
			} else {
				setVolume(1)
			}
			bar.SetMuted(volume == 0)
		}
		acts.SeekTo = func(f float64) { seekTo(seekTarget(f, info.Duration)) }
	}
	bar = newControlBar(acts)
	bar.Layout(fbW, fbH, len(v.maps))

	// One composition, shared with the tests that drive it. Activity only records
	// WHEN something happened; Tick is the single place that decides whether the
	// bar is up.
	pointer := newPointerLayer()
	pointer.Layout(fbW, fbH, len(v.maps))
	ctrl := newControlsOverlay(surface, bar.Root(), pointer)
	overlay = ctrl.Overlay
	noteActivity = ctrl.Note
	notePointer = pointer.Moved

	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if ctrl.Tick() {
					repaint()
				}
			}
		}
	}()

	if clock != nil {
		// The first frame is already in hand; show it, then let the clock run.
		v.composeInto(v.front, first)
		first.Release()
		shown = 1
		clock.Play()
		cfg.logf("playing with sound")

		// Nothing pushes a frame at us: the player is asked. So a repaint is
		// asked for on a ticker, and the paint decides whether the picture has
		// moved on. The rate is the display's, not the video's -- asking more
		// often than the video changes costs one poll that answers nothing.
		go func() {
			t := time.NewTicker(time.Second / 60)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					// The bar reads the clock rather than being told: one source of
					// truth, and a seek made with the keyboard moves the slider too.
					bar.SetPlaying(!pause.Paused())
					bar.SetProgress(clock.CurrentTime(), info.Duration)
					repaint()
					// AVPlayer has no end-of-playback signal, so the end is
					// noticed by the clock reaching the duration.
					if d := info.Duration; d > 0 && clock.CurrentTime() >= d-100*time.Millisecond {
						cfg.logf("end of file after %d frames", shown)
						closeStop()
						return
					}
				}
			}
		}()
	} else {
		go func() {
			v.decode(play, first, cfg, stop, repaint, pause, bar, info.Duration, overlay, theme)
			// Decoding is what the window is for, so when it ends the window should
			// go too -- otherwise a finished file leaves a black rectangle over the
			// display with no way out but a keypress.
			closeStop()
		}()
	}

	// Run returns when the window closes. Nothing closes it from the outside
	// yet, so a stop has to be turned into one.
	go func() {
		<-stop
		win.Close()
	}()
	return win.Run(overlay)
}

// view holds the composed framebuffer and the tables that fill it.
type view struct {
	fbW, fbH   int
	eyeW, eyeH int
	maps       []*warp.Map
	coverage   float64

	mu    sync.Mutex
	front []uint32 // shown
	back  []uint32 // being composed
	bytes []byte   // a byte view of front, handed to the toolkit
}

// newView builds the sampling tables for the framebuffer it will fill.
func newView(fbW, fbH int, stereoscopic bool, geom Geometry, info SourceInfo, strideWords int, fovy float64) *view {
	v := &view{fbW: fbW, fbH: fbH}
	v.front = make([]uint32, fbW*fbH)
	v.back = make([]uint32, fbW*fbH)
	for i := range v.front {
		v.front[i] = black
	}

	eyes := []stereo.Eye{stereo.Left}
	v.eyeW, v.eyeH = fbW, fbH
	if stereoscopic {
		eyes = []stereo.Eye{stereo.Left, stereo.Right}
		v.eyeW = fbW / 2
	}

	vp := projection.Viewport{Width: v.eyeW, Height: v.eyeH, FOVyDeg: fovy}
	total := 0
	for _, eye := range eyes {
		m := warp.Build(vp, geom.Projection, pose.Identity(), warp.Source{
			Width:  info.Width,
			Height: info.Height,
			Stride: strideWords,
			Eye:    geom.Format.EyeRect(eye, info.Width, info.Height),
		})
		v.maps = append(v.maps, m)
		total += m.Covered()
	}
	if n := v.eyeW * v.eyeH * len(eyes); n > 0 {
		v.coverage = float64(total) / float64(n)
	}
	return v
}

// frame is the toolkit's Surface callback: the pixels to show, right now.
func (v *view) frame() ([]byte, int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bytes == nil {
		v.bytes = asBytes(v.front)
	}
	return v.bytes, v.fbW, v.fbH
}

// present publishes the composed frame.
//
// The lock is held for the swap and for the whole of the paint that reads the
// front buffer. That does mean a decode can wait on a paint — which is fine
// here, and deliberate: decoding runs at many times real time (72x was measured
// for 720p), so the cheap and obviously-correct arrangement wins over a
// triple buffer that would need reasoning about.
func (v *view) present() {
	v.mu.Lock()
	v.front, v.back = v.back, v.front
	v.bytes = asBytes(v.front)
	v.mu.Unlock()
}

// decode pulls frames, waits for each one's moment, warps it and presents it.
func (v *view) decode(src source, first *srcFrame, cfg Config, stop <-chan struct{}, repaint func(), pause *pauser, bar *controlBar, total time.Duration, snapRoot toolkit.Widget, snapTheme *toolkit.Theme) {
	var (
		start  time.Time
		shown  int
		late   time.Duration
		frames int
	)
	start = time.Now()
	for {
		select {
		case <-stop:
			cfg.logf("stopped after %d frames", shown)
			return
		default:
		}

		var f *srcFrame
		var err error
		if first != nil {
			f, first = first, nil
		} else {
			f, err = src.Next()
		}
		if errors.Is(err, io.EOF) {
			cfg.logf("end of file after %d frames (mean lateness %v)", shown, meanLate(late, frames))
			if !cfg.Loop {
				return
			}
			// Reopening is the honest way to loop until seeking exists.
			cfg.logf("looping")
			return
		}
		if err != nil {
			// A stop closes the source from under this goroutine, so a failure
			// here is expected during shutdown and reporting it would print an
			// alarming line for an ordinary quit.
			select {
			case <-stop:
			default:
				cfg.logf("decode stopped: %v", err)
			}
			return
		}

		// Wait for this frame's moment. Being late is normal and recoverable;
		// being early and not waiting would play the file as fast as it decodes.
		// Wait out a pause before timing anything: the stopped time is given back
		// through the offset, so a resumed video carries on rather than racing.
		pause.Wait(stop)
		// Follow the sound when there is any. Timing video against the wall clock
		// instead would drift away from the audio over a feature-length film, and
		// the ear notices that long before the eye does.
		elapsed := time.Since(start) - pause.Offset()
		if ac, ok := src.(audioClocked); ok {
			if at, has := ac.AudioClock(); has {
				elapsed = at
			}
		}
		if d := f.PTS - elapsed; d > 0 {
			select {
			case <-time.After(d):
			case <-stop:
				f.Release()
				return
			}
		} else {
			late += -d
		}
		frames++

		pts := f.PTS
		for i, m := range v.maps {
			m.ApplySwapRB(f.Pix, v.back, v.fbW, i*v.eyeW, black)
		}
		f.Release()
		v.present()
		if bar != nil {
			bar.SetPlaying(!pause.Paused())
			bar.SetProgress(pts, total)
		}
		repaint()
		shown++

		if cfg.Snapshot != "" && shown == cfg.SnapshotAfter+1 {
			if err := v.writeSnapshot(cfg.Snapshot, snapRoot, snapTheme); err != nil {
				cfg.logf("snapshot failed: %v", err)
			} else {
				cfg.logf("snapshot of frame %d written to %s", shown-1, cfg.Snapshot)
			}
		}
	}
}

// writeSnapshot saves what the viewer sees as a PNG.
//
// It draws the WHOLE widget tree -- the video and whatever is layered over it --
// through the same painter the window uses, rather than copying the video
// buffer alone. A snapshot that shows only the video cannot answer the one
// question worth asking about an overlay: is it there?
func (v *view) writeSnapshot(path string, root toolkit.Widget, theme *toolkit.Theme) error {
	img := image.NewRGBA(image.Rect(0, 0, v.fbW, v.fbH))
	if root != nil {
		root.Draw(painter.NewPixelPainter(img.Pix, v.fbW, v.fbH), theme)
	} else {
		v.mu.Lock()
		copy(img.Pix, asBytes(v.front))
		v.mu.Unlock()
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// meanLate reports the average lateness, guarding the empty case.
func meanLate(total time.Duration, n int) time.Duration {
	if n == 0 {
		return 0
	}
	return total / time.Duration(n)
}

// composeInto warps a frame into dst, one eye at a time.
func (v *view) composeInto(dst []uint32, f *srcFrame) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, m := range v.maps {
		m.ApplySwapRB(f.Pix, dst, v.fbW, i*v.eyeW, black)
	}
	v.bytes = asBytes(dst)
}

// firstFrameTimeout bounds the wait for a clocked source's first picture. It is
// generous: a cold file on a slow disk takes a moment, and failing early would
// blame the player for the storage.
const firstFrameTimeout = 10 * time.Second

// firstFrame gets one picture out of a source, which is what teaches the warp
// tables the decoder's row stride.
//
// A pull source hands one over on request. A clocked source has to be asked
// repeatedly while the MAIN run loop is pumped, because that is what loads the
// file — and if it still has nothing, it is started, since some items vend no
// buffer until the clock moves. It is muted for that: a file should not blurt
// its first half-second of sound before the window is even on screen.
func firstFrame(src source, clock clocked) (*srcFrame, error) {
	if clock == nil {
		f, err := src.Next()
		if err != nil {
			return nil, err
		}
		if f == nil {
			// A pull source always has a frame or an error. Getting neither means
			// a POLL source arrived here, which happens when a clocked type
			// quietly stops satisfying the interface -- a missing method makes
			// the assertion in Play fail silently. Say so instead of handing
			// back a nil the caller will dereference.
			return nil, fmt.Errorf("the source yielded no frame and no error; it is a poll source taking the pull path")
		}
		return f, nil
	}
	clock.SetVolume(0)
	deadline := time.Now().Add(firstFrameTimeout)
	started := false
	for time.Now().Before(deadline) {
		clock.Pump(20 * time.Millisecond)
		f, err := src.Next()
		if err != nil {
			return nil, err
		}
		if f != nil {
			clock.Pause()
			clock.SetVolume(1)
			return f, nil
		}
		if !started && time.Now().Add(firstFrameTimeout/2).After(deadline) {
			started = true
			clock.Play()
		}
	}
	clock.Pause()
	clock.SetVolume(1)
	return nil, fmt.Errorf("no picture within %v; the item never became ready", firstFrameTimeout)
}
