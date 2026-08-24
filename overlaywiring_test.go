package player

import (
	"testing"
	"time"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// TestControlsOverlayAppearsHidesAndComesBack drives the composition the player
// runs, from a pointer event all the way to pixels, over a full cycle.
//
// This is the test that should have existed from the start. Every piece had its
// own test and every piece passed: the bar laid out, the bar rendered, the
// events routed. What had no test was the SEQUENCE — appear, hide, appear again
// — and that is the only place the bug was. The bar came up once at startup and
// never again, because noting activity flipped the same flag the ticker compared
// against, so the ticker concluded nothing had changed and pushed nothing.
//
// So this walks the sequence, and it looks at pixels rather than at flags: a
// flag saying "shown" is not a control bar on the screen.
func TestControlsOverlayAppearsHidesAndComesBack(t *testing.T) {
	const (
		fbW, fbH = 1920, 1080
		eyes     = 1
	)
	// A flat, distinctive video frame, so anything else in the bar's rows is
	// unmistakably the controls and not the film.
	const vidR, vidG, vidB = 10, 120, 60
	video := make([]byte, fbW*fbH*4)
	for i := 0; i < len(video); i += 4 {
		video[i], video[i+1], video[i+2], video[i+3] = vidR, vidG, vidB, 255
	}

	surface := toolkit.NewSurface(func() ([]byte, int, int) { return video, fbW, fbH })

	bar := newControlBar(barActions{
		Restart: func() {}, Back: func() {}, TogglePause: func() {},
		Forward: func() {}, ToggleMute: func() {}, SeekTo: func(float64) {},
	})
	bar.Layout(fbW, fbH, eyes)
	bar.SetPlaying(true)
	bar.SetProgress(37*time.Minute+12*time.Second, 43*time.Minute+58*time.Second)

	ctrl := newControlsOverlay(surface, bar.Root())
	// Wired exactly as the player wires it: a pointer event on the video is
	// activity.
	surface.OnInput = func(toolkit.Event) { ctrl.Note() }

	// A test clock, so the three idle seconds do not have to be waited out.
	now := time.Unix(10_000, 0)
	ctrl.vis.now = func() time.Time { return now }
	ctrl.vis.last = now

	ctrl.Overlay.SetBounds(toolkit.Rect{W: fbW, H: fbH})

	region := layoutBar(fbW, fbH, eyes)

	// barInk counts pixels inside the bar's rectangle that are not the video, by
	// drawing the whole composition the way the window would.
	barInk := func() int {
		buf := make([]byte, fbW*fbH*4)
		for i := 0; i < len(buf); i += 4 {
			buf[i], buf[i+1], buf[i+2], buf[i+3] = 0, 0, 0, 255
		}
		p := painter.NewPixelPainter(buf, fbW, fbH)
		ctrl.Overlay.Draw(p, toolkit.DefaultDark())

		n := 0
		for y := region.Y; y < region.Y+region.H; y++ {
			for x := region.X; x < region.X+region.W; x++ {
				o := (y*fbW + x) * 4
				if buf[o] != vidR || buf[o+1] != vidG || buf[o+2] != vidB {
					n++
				}
			}
		}
		return n
	}

	total := region.W * region.H

	// 1. It starts up, so a viewer sees what the controls are before they hide.
	if !ctrl.Tick() {
		t.Fatal("the first Tick reported no change; the bar is never pushed and the viewer sees nothing")
	}
	up := barInk()
	if up < total/4 {
		t.Fatalf("bar drawn over %d of %d pixels in its own rectangle; that is not a control bar", up, total)
	}

	// 2. Left alone, it goes away.
	now = now.Add(hideAfter + time.Second)
	if !ctrl.Tick() {
		t.Fatal("the bar did not hide after the idle timeout")
	}
	if n := barInk(); n != 0 {
		t.Fatalf("the bar's rectangle still has %d non-video pixels after hiding; it did not go away", n)
	}

	// 3. THE REGRESSION. The pointer moves over the video, which is the whole
	// point of the feature, and the bar must come back — through the real event
	// path, not by calling Note directly.
	ctrl.Overlay.OnEvent(toolkit.Event{Kind: toolkit.EventMouseMove, X: fbW / 2, Y: fbH / 3})
	if !ctrl.Tick() {
		t.Fatal("a pointer move over the video did not bring the controls back; " +
			"this is the bug the viewer reported — the bar appears once and never again")
	}
	if n := barInk(); n < total/4 {
		t.Fatalf("after the pointer moved, the bar covers %d of %d pixels; it is not really back", n, total)
	}

	// 4. And it hides again, so this is a cycle rather than a latch.
	now = now.Add(hideAfter + time.Second)
	if !ctrl.Tick() {
		t.Fatal("the bar did not hide again on the second idle stretch")
	}
	if n := barInk(); n != 0 {
		t.Fatalf("the bar's rectangle has %d non-video pixels on the second hide", n)
	}
}

// TestControlsOverlayStaysUpWhileThePointerIsOverIt covers the case the
// activityWidget exists for: while the pointer is on the bar, the events go to
// the bar and never reach the video underneath, so without it the controls would
// vanish from under the hand using them.
func TestControlsOverlayStaysUpWhileThePointerIsOverIt(t *testing.T) {
	const fbW, fbH = 1920, 1080

	surface := toolkit.NewSurface(func() ([]byte, int, int) { return nil, 0, 0 })
	bar := newControlBar(barActions{TogglePause: func() {}})
	bar.Layout(fbW, fbH, 1)

	ctrl := newControlsOverlay(surface, bar.Root())
	surface.OnInput = func(toolkit.Event) {
		t.Error("an event landed on the VIDEO while the pointer was over the bar")
	}

	now := time.Unix(0, 0)
	ctrl.vis.now = func() time.Time { return now }
	ctrl.vis.last = now
	ctrl.Overlay.SetBounds(toolkit.Rect{W: fbW, H: fbH})
	ctrl.Tick() // put it up

	region := layoutBar(fbW, fbH, 1)
	onBar := toolkit.Event{
		Kind: toolkit.EventMouseMove,
		X:    region.X + region.W/2,
		Y:    region.Y + region.H/2,
	}

	// Keep the pointer on the bar for well past the timeout.
	for i := 0; i < 10; i++ {
		now = now.Add(hideAfter / 2)
		ctrl.Overlay.OnEvent(onBar)
		if ctrl.Tick() {
			t.Fatalf("iteration %d: the controls changed state while the pointer rested on them", i)
		}
		if !ctrl.Shown() {
			t.Fatalf("iteration %d: the controls hid from under the pointer using them", i)
		}
	}
}
