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
	"unsafe"

	"github.com/go-macos/avfoundation"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/window"
	"github.com/go-xrkit/xrkit/pose"
	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
	"github.com/go-xrkit/xrkit/warp"
)

// black is opaque black as a little-endian RGBA word: the bytes in memory are
// R=0, G=0, B=0, A=255. It fills whatever the content does not cover.
const black uint32 = 0xff000000

// Config parametrises [Play].
type Config struct {
	// Path is the video file.
	Path string
	// Screen names the display to play on, matched case-insensitively against
	// the attached displays. Empty picks automatically — recognised glasses
	// first. See [ChooseDisplay].
	Screen string
	// Geometry overrides what the content is. Nil detects it from the file name
	// and frame shape; see [Detect].
	Geometry *Geometry
	// FOVyDeg is the vertical field of view of each eye's view, in degrees.
	// Zero uses 90, which is about what these glasses present.
	FOVyDeg float64
	// Loop restarts at the end instead of stopping.
	Loop bool
	// Mono forces a single-eye view even on a display that looks stereoscopic.
	// Useful for looking at the result on an ordinary monitor.
	Mono bool
	// Log, when set, receives one line of progress per notable event.
	Log func(string)
	// For, when positive, stops playback after this long. A full-screen window
	// with no chrome is awkward to end from the outside, and an automated check
	// cannot press a key at all.
	For time.Duration
	// Snapshot, when set, names a PNG to write once Frames have been shown. It
	// captures the composed framebuffer -- the exact pixels sent to the glasses --
	// which is the one form of visual proof that needs no screen-recording
	// consent.
	Snapshot string
	// SnapshotAfter is how many frames to show before taking the snapshot.
	// Zero means the first frame.
	SnapshotAfter int
}

// fovOrDefault resolves the requested field of view.
func (c Config) fovOrDefault() float64 {
	if c.FOVyDeg <= 0 {
		return 90
	}
	return c.FOVyDeg
}

func (c Config) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(fmt.Sprintf(format, args...))
	}
}

// Play opens the file, puts a full-screen window on the chosen display, and
// plays until the file ends or the window closes.
//
// It must be called from the process main goroutine: the window back-end pins
// that thread for AppKit, which requires it.
func Play(cfg Config) error {
	// The decoder pads rows, and by how much is not knowable until a frame comes
	// out -- but the sampling tables bake the stride into every offset, so it has
	// to be known BEFORE they are built. Decoding one frame and throwing it away
	// costs a millisecond; recomputing four million offsets per frame to fix the
	// stride afterwards would cost the whole benefit of having a table.
	strideWords, err := probeStride(cfg.Path)
	if err != nil {
		return err
	}

	dec, err := avfoundation.Open(cfg.Path)
	if err != nil {
		return err
	}
	defer dec.Close()
	info := dec.Info()

	screens, err := window.Screens()
	if err != nil {
		return fmt.Errorf("player: cannot enumerate displays: %w", err)
	}
	displays := make([]Display, len(screens))
	for i, s := range screens {
		displays[i] = Display{Name: s.Name, Width: s.Width, Height: s.Height, Primary: s.Primary}
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
	geom := Detect(cfg.Path, info.Width, info.Height)
	if cfg.Geometry != nil {
		geom = *cfg.Geometry
		geom.Why = "given by the caller"
	}

	cfg.logf("file      %s", cfg.Path)
	cfg.logf("  %dx%d  %.3f fps  %v", info.Width, info.Height, info.FrameRate, info.Duration.Round(time.Millisecond))
	cfg.logf("content   %s", geom)
	cfg.logf("  because: %s", geom.Why)
	cfg.logf("display   %s", chosen)
	if stereoscopic {
		cfg.logf("  side-by-side 3D mode: one eye per half")
	} else {
		cfg.logf("  single view (the panel is not in a side-by-side mode)")
	}

	win, err := window.Open(window.Config{
		Title: "xrplay",
		// A self-rendering root composes its own pixels at whatever size it is
		// handed, which is exactly the case NativeScale is for.
		RenderScale: window.NativeScale,
		Screen:      &target,
		Fullscreen:  true,
		Theme:       toolkit.DefaultDark(),
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

	v := newView(fbW, fbH, stereoscopic, geom, info, strideWords, cfg.fovOrDefault())
	cfg.logf("view      %d eye(s) of %dx%d, %.0f deg vertical, %.1f%% of the view covered",
		len(v.maps), v.eyeW, v.eyeH, cfg.fovOrDefault(), v.coverage*100)

	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }
	defer closeStop()

	surface := toolkit.NewSurface(v.frame)
	surface.OnInput = func(ev toolkit.Event) {
		// Any key ends playback: this is a full-screen window with no chrome, so
		// there has to be an obvious way out.
		if ev.Kind == toolkit.EventKeyDown {
			closeStop()
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

	go func() {
		v.decode(dec, cfg, stop, repaint)
		// Decoding is what the window is for, so when it ends the window should
		// go too -- otherwise a finished file leaves a black rectangle over the
		// display with no way out but a keypress.
		closeStop()
	}()

	// Run returns when the window closes. Nothing closes it from the outside
	// yet, so a stop has to be turned into one.
	go func() {
		<-stop
		win.Close()
	}()
	return win.Run(surface)
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
func newView(fbW, fbH int, stereoscopic bool, geom Geometry, info avfoundation.Info, strideWords int, fovy float64) *view {
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
func (v *view) decode(dec *avfoundation.Reader, cfg Config, stop <-chan struct{}, repaint func()) {
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

		f, err := dec.NextFrame()
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
			cfg.logf("decode stopped: %v", err)
			return
		}

		// Wait for this frame's moment. Being late is normal and recoverable;
		// being early and not waiting would play the file as fast as it decodes.
		if d := f.PTS - time.Since(start); d > 0 {
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

		src := asWords(f.Pix)
		for i, m := range v.maps {
			m.ApplySwapRB(src, v.back, v.fbW, i*v.eyeW, black)
		}
		f.Release()
		v.present()
		repaint()
		shown++

		if cfg.Snapshot != "" && shown == cfg.SnapshotAfter+1 {
			if err := v.writeSnapshot(cfg.Snapshot); err != nil {
				cfg.logf("snapshot failed: %v", err)
			} else {
				cfg.logf("snapshot of frame %d written to %s", shown-1, cfg.Snapshot)
			}
		}
	}
}

// writeSnapshot saves the composed framebuffer as a PNG. It is the pixels the
// glasses were actually given, read back under the same lock the painter uses.
func (v *view) writeSnapshot(path string) error {
	v.mu.Lock()
	img := image.NewRGBA(image.Rect(0, 0, v.fbW, v.fbH))
	copy(img.Pix, asBytes(v.front))
	v.mu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// probeStride decodes one frame to learn the decoder.s row padding, in 32-bit
// words, then throws it away.
func probeStride(path string) (int, error) {
	r, err := avfoundation.Open(path)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	f, err := r.NextFrame()
	if err != nil {
		return 0, fmt.Errorf("player: cannot decode a first frame of %s: %w", path, err)
	}
	stride := f.Stride
	f.Release()
	if stride <= 0 || stride%4 != 0 {
		return 0, fmt.Errorf("player: the decoder reported a %d-byte stride, which is not a whole number of pixels", stride)
	}
	return stride / 4, nil
}

// meanLate reports the average lateness, guarding the empty case.
func meanLate(total time.Duration, n int) time.Duration {
	if n == 0 {
		return 0
	}
	return total / time.Duration(n)
}

// asWords views a byte slice as 32-bit words. The decoder's buffers are at least
// 16-byte aligned, so the alignment a uint32 needs is satisfied.
func asWords(b []byte) []uint32 {
	if len(b) < 4 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), len(b)/4)
}

// asBytes is the reverse, for handing a composed frame to the toolkit.
func asBytes(w []uint32) []byte {
	if len(w) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&w[0])), len(w)*4)
}
