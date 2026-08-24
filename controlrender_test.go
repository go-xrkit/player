package player

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// TestRenderControlBar draws the bar offscreen and asserts it actually put ink
// where it said it would.
//
// A layout test proves the rectangles are sane; it cannot prove anything was
// drawn in them. This renders the real widget tree through the real painter and
// counts pixels that differ from the ground, which is the difference between "my
// arithmetic is right" and "there is a control bar".
//
// With XRPLAY_RENDER_DIR set it also writes the PNGs, so a human can look.
func TestRenderControlBar(t *testing.T) {
	const groundR, groundG, groundB = 40, 44, 52

	for _, tc := range []struct {
		name string
		w, h int
		eyes int
	}{
		{"1920x1080-1eye", 1920, 1080, 1},
		{"3840x1080-2eyes", 3840, 1080, 2},
	} {
		buf := make([]byte, tc.w*tc.h*4)
		for i := 0; i < len(buf); i += 4 {
			buf[i], buf[i+1], buf[i+2], buf[i+3] = groundR, groundG, groundB, 255
		}
		p := painter.NewPixelPainter(buf, tc.w, tc.h)

		bar := newControlBar(barActions{
			Restart:     func() {},
			Back:        func() {},
			TogglePause: func() {},
			Forward:     func() {},
			ToggleMute:  func() {},
			SeekTo:      func(float64) {},
		})
		bar.Layout(tc.w, tc.h, tc.eyes)
		bar.SetPlaying(true)
		bar.SetProgress(37*time.Minute+12*time.Second, 43*time.Minute+58*time.Second)
		bar.Root().Draw(p, toolkit.DefaultDark())

		region := layoutBar(tc.w, tc.h, tc.eyes)

		// The scrim fills the bar by design, so counting "not the ground" would
		// only prove a rectangle was filled. What matters is ink that stands OUT
		// from the scrim: the icons, the text and the slider. The scrim colour is
		// sampled from a corner of the bar rather than assumed, so this holds
		// whether or not the painter blends the alpha.
		at := func(x, y int) [3]byte {
			i := (y*tc.w + x) * 4
			return [3]byte{buf[i], buf[i+1], buf[i+2]}
		}
		// Sample well inside the scrim: left of the centred buttons and below the
		// slider row. A corner would land on the rounded edge's anti-aliasing and
		// make almost every other pixel look like ink.
		scrim := at(region.X+region.W/4, region.Y+region.H-12)
		ground := [3]byte{groundR, groundG, groundB}
		if scrim == ground {
			t.Fatalf("%s: the bar drew NO scrim; its corner is still the ground", tc.name)
		}

		controls, outside := 0, 0
		for y := 0; y < tc.h; y++ {
			for x := 0; x < tc.w; x++ {
				c := at(x, y)
				if c == ground {
					continue
				}
				inside := x >= region.X && x < region.X+region.W &&
					y >= region.Y && y < region.Y+region.H
				if !inside {
					outside++
					continue
				}
				if c != scrim {
					controls++
				}
			}
		}

		if controls == 0 {
			t.Errorf("%s: the bar drew a scrim and NOTHING on it", tc.name)
		}
		// It must stay inside the rectangle it claimed, or it would paint over
		// video it does not own -- and in a side-by-side mode, into the other eye.
		if outside != 0 {
			t.Errorf("%s: %d pixels painted OUTSIDE the bar rect %+v", tc.name, outside, region)
		}
		// Controls are sparse marks on a ground, not a second slab.
		if area := region.W * region.H; controls > area/2 {
			t.Errorf("%s: %d of %d pixels are control ink; that is not a control bar",
				tc.name, controls, area)
		}
		t.Logf("%s: %d pixels of control ink on a %dx%d scrim", tc.name, controls, region.W, region.H)

		if dir := os.Getenv("XRPLAY_RENDER_DIR"); dir != "" {
			img := &image.RGBA{Pix: buf, Stride: tc.w * 4, Rect: image.Rect(0, 0, tc.w, tc.h)}
			f, err := os.Create(dir + "/bar-" + tc.name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatal(err)
			}
			f.Close()
			t.Logf("wrote %s/bar-%s.png", dir, tc.name)
		}
	}
}
