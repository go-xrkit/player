package player

import (
	"testing"
	"time"
)

// TestLayoutBarStaysOnScreen is the property worth checking without a headset:
// a control bar that lands off the panel, or under the blind edge the optics
// eat, is not something to discover while wearing it.
func TestLayoutBarStaysOnScreen(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fbW, fbH int
		eyes     int
	}{
		{"Beast in 2D", 1920, 1080, 1},
		{"Beast in side-by-side 3D", 3840, 1080, 2},
		{"a scaled mode", 5120, 1600, 2},
		{"a laptop panel", 2056, 1329, 1},
		{"something small", 640, 480, 1},
	} {
		r := layoutBar(tc.fbW, tc.fbH, tc.eyes)
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("%s: empty bar %+v", tc.name, r)
			continue
		}
		if r.X < 0 || r.Y < 0 {
			t.Errorf("%s: bar starts off-screen at %+v", tc.name, r)
		}
		if r.Y+r.H > tc.fbH {
			t.Errorf("%s: bar bottom %d is below the panel %d", tc.name, r.Y+r.H, tc.fbH)
		}
		// In a side-by-side mode the bar must stay inside the LEFT eye's half:
		// spanning the panel would draw it twice, at different depths, and read
		// as a double image.
		if limit := tc.fbW / tc.eyes; r.X+r.W > limit {
			t.Errorf("%s: bar right edge %d crosses into the other eye at %d",
				tc.name, r.X+r.W, limit)
		}
		// And it must keep clear of the very edge.
		if r.X < barMargin || tc.fbH-(r.Y+r.H) < barMargin {
			t.Errorf("%s: bar %+v is flush with an edge the optics eat", tc.name, r)
		}
	}
}

func TestLayoutBarRefusesDegenerateSizes(t *testing.T) {
	for _, tc := range [][3]int{{0, 100, 1}, {100, 0, 1}, {-1, -1, 1}} {
		if r := layoutBar(tc[0], tc[1], tc[2]); r != (Rect{}) {
			t.Errorf("layoutBar%v = %+v, want the zero rect", tc, r)
		}
	}
	// A panel shorter than the bar, and one narrower than its own margins: both
	// must clamp to something drawable rather than produce a negative rectangle
	// that a painter would read as a wrap-around.
	for _, tc := range [][3]int{{1920, 40, 1}, {60, 1080, 1}, {200, 1080, 8}, {1920, 100, 1}} {
		r := layoutBar(tc[0], tc[1], tc[2])
		if r.W < 0 || r.H < 0 || r.X < 0 || r.Y < 0 {
			t.Errorf("layoutBar%v = %+v, want no negative field", tc, r)
		}
		if r.H > tc[1] {
			t.Errorf("layoutBar%v = %+v, taller than the %d-pixel panel", tc, r, tc[1])
		}
	}

	// A nonsense eye count must not divide by zero or invert the width.
	if r := layoutBar(1920, 1080, 0); r.W <= 0 {
		t.Errorf("layoutBar with 0 eyes = %+v, want a usable bar", r)
	}
	if r := layoutBar(1920, 1080, -3); r.W <= 0 {
		t.Errorf("layoutBar with negative eyes = %+v, want a usable bar", r)
	}
}

func TestFormatTime(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{0, "0:00"},
		{5 * time.Second, "0:05"},
		{65 * time.Second, "1:05"},
		{10 * time.Minute, "10:00"},
		{59*time.Minute + 59*time.Second, "59:59"},
		{time.Hour, "1:00:00"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{43*time.Minute + 58*time.Second, "43:58"},
		// A negative position is nonsense, not a reason to print a minus sign.
		{-5 * time.Second, "0:00"},
		// Sub-second precision is dropped, not rounded up: a player showing 0:01
		// at 0.6 s looks ahead of itself.
		{1500 * time.Millisecond, "0:01"},
	} {
		if got := formatTime(tc.in); got != tc.want {
			t.Errorf("formatTime(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSeekTargetAndProgressAreInverses(t *testing.T) {
	const total = 100 * time.Second
	for _, f := range []float64{0, 0.25, 0.5, 0.99, 1} {
		at := seekTarget(f, total)
		if back := progressFraction(at, total); back < f-1e-9 || back > f+1e-9 {
			t.Errorf("fraction %v -> %v -> %v", f, at, back)
		}
	}
	// Both clamp rather than running past the file.
	if got := seekTarget(-1, total); got != 0 {
		t.Errorf("seekTarget(-1) = %v, want 0", got)
	}
	if got := seekTarget(2, total); got != total {
		t.Errorf("seekTarget(2) = %v, want the duration", got)
	}
	if got := progressFraction(-time.Second, total); got != 0 {
		t.Errorf("progressFraction of a negative position = %v, want 0", got)
	}
	if got := progressFraction(2*total, total); got != 1 {
		t.Errorf("progressFraction past the end = %v, want 1", got)
	}
	// A file of unknown length has no meaningful position, and must not divide
	// by zero to find that out.
	if got := seekTarget(0.5, 0); got != 0 {
		t.Errorf("seekTarget on a zero-length file = %v, want 0", got)
	}
	if got := progressFraction(time.Second, 0); got != 0 {
		t.Errorf("progressFraction on a zero-length file = %v, want 0", got)
	}
}
