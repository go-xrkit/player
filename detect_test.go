package player

import (
	"strings"
	"testing"

	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
)

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name       string
		file       string
		w, h       int
		wantLayout stereo.Layout
		wantKind   projection.Kind
		wantHSpan  float64
		wantSwap   bool
	}{
		// The shapes real files come in, deduced from dimensions alone.
		{"4K monoscopic sphere", "beach.mp4", 3840, 1920,
			stereo.Mono, projection.Equirect, 360, false},
		// A 4:1 frame is a FULL sphere per eye: stereoscopic 360, not VR180.
		{"stereoscopic 360, side by side", "concert.mp4", 7680, 1920,
			stereo.SideBySide, projection.Equirect, 360, false},
		// Real VR180 is 180x180 per eye, which is square, so the frame is 2:1 --
		// indistinguishable by aspect from a monoscopic sphere. Only the name can
		// tell them apart, which is exactly why the name is consulted first.
		{"VR180 side by side, named", "concert_vr180_sbs.mp4", 3840, 1920,
			stereo.SideBySide, projection.Equirect, 180, false},
		{"the same frame unnamed is a monoscopic sphere", "concert.mp4", 3840, 1920,
			stereo.Mono, projection.Equirect, 360, false},
		{"square frame is two stacked spheres", "stacked.mp4", 3840, 3840,
			stereo.OverUnder, projection.Equirect, 360, false},
		{"ordinary 16:9 is a flat screen", "talk.mp4", 1920, 1080,
			stereo.Mono, projection.Flat, 60, false},

		// A name always beats the aspect ratio: someone knew.
		{"name says 180 over a 2:1 frame", "dive_180.mp4", 3840, 1920,
			stereo.Mono, projection.Equirect, 180, false},
		{"name says sbs over a 16:9 frame", "film_sbs.mp4", 1920, 1080,
			stereo.SideBySide, projection.Flat, 60, false},
		{"name says 3d", "movie-3D.mkv", 1920, 1080,
			stereo.SideBySide, projection.Flat, 60, false},
		{"name says over-under", "clip_ou.mp4", 1920, 1080,
			stereo.OverUnder, projection.Flat, 60, false},
		{"name says fisheye", "lens_fisheye_sbs.mp4", 3840, 1080,
			stereo.SideBySide, projection.Fisheye, 180, false},
		{"name says mono over a 4:1 frame", "wide_mono.mp4", 7680, 1920,
			stereo.Mono, projection.Flat, 60, false},
		{"name says the eyes are swapped", "movie_sbs_rl.mp4", 1920, 1080,
			stereo.SideBySide, projection.Flat, 60, true},
		{"vr180 in the name", "vr180-clip.mp4", 4096, 4096,
			stereo.OverUnder, projection.Equirect, 180, false},

		// A square EYE with no projection word in the name: the aspect alone says
		// 180x180, which is how VR180 equirectangular is stored.
		{"square eye, unnamed projection", "clip_sbs.mp4", 3840, 1920,
			stereo.SideBySide, projection.Equirect, 180, false},
		{"square frame declared mono", "art_mono.mp4", 2048, 2048,
			stereo.Mono, projection.Equirect, 180, false},

		// Case and path must not matter.
		{"upper case and a full path", "/Films/HOLIDAY_360_SBS.MP4", 7680, 1920,
			stereo.SideBySide, projection.Equirect, 360, false},
	} {
		g := Detect(tc.file, tc.w, tc.h)
		if g.Format.Layout != tc.wantLayout {
			t.Errorf("%s: layout = %v, want %v  (why: %s)", tc.name, g.Format.Layout, tc.wantLayout, g.Why)
		}
		if g.Projection.Kind != tc.wantKind {
			t.Errorf("%s: kind = %v, want %v  (why: %s)", tc.name, g.Projection.Kind, tc.wantKind, g.Why)
		}
		if g.Projection.HSpanDeg != tc.wantHSpan {
			t.Errorf("%s: hspan = %v, want %v  (why: %s)", tc.name, g.Projection.HSpanDeg, tc.wantHSpan, g.Why)
		}
		if g.Format.Swapped != tc.wantSwap {
			t.Errorf("%s: swapped = %v, want %v", tc.name, g.Format.Swapped, tc.wantSwap)
		}
		if g.Why == "" {
			t.Errorf("%s: no reasoning recorded; a wrong guess must be explicable", tc.name)
		}
	}
}

func TestDetectHandlesDegenerateDimensions(t *testing.T) {
	for _, d := range [][2]int{{0, 0}, {-1, 100}, {100, 0}} {
		g := Detect("x.mp4", d[0], d[1])
		// Whatever it decides, it must decide something coherent and say why.
		if g.Why == "" {
			t.Errorf("%v: no reasoning recorded", d)
		}
		if g.Projection.HSpanDeg <= 0 {
			t.Errorf("%v: produced a non-positive span %v", d, g.Projection.HSpanDeg)
		}
	}
}

func TestGeometryString(t *testing.T) {
	g := Detect("clip_sbs_rl_360.mp4", 3840, 1080)
	s := g.String()
	for _, want := range []string{"equirectangular", "side-by-side", "swapped"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, want it to mention %q", s, want)
		}
	}
	// A monoscopic geometry must not claim swapped eyes.
	if s := Detect("beach.mp4", 3840, 1920).String(); strings.Contains(s, "swapped") {
		t.Errorf("String() = %q, should not mention swapping", s)
	}
}

func TestNearAspect(t *testing.T) {
	for _, tc := range []struct {
		w, h, num, den int
		want           bool
	}{
		{3840, 1920, 2, 1, true},
		{3840, 1918, 2, 1, true},  // a couple of rows cropped
		{1920, 1080, 2, 1, false}, // 16:9 must never read as 2:1
		{4096, 4096, 1, 1, true},
		{7680, 1920, 4, 1, true},
		{0, 100, 2, 1, false},
		{100, 0, 2, 1, false},
		{100, 100, 1, 0, false},
	} {
		if got := nearAspect(tc.w, tc.h, tc.num, tc.den); got != tc.want {
			t.Errorf("nearAspect(%d,%d,%d,%d) = %v, want %v",
				tc.w, tc.h, tc.num, tc.den, got, tc.want)
		}
	}
}

func TestHasAny(t *testing.T) {
	if !hasAny("holiday_sbs.mp4", "_ou", "_sbs") {
		t.Error("hasAny missed a present substring")
	}
	if hasAny("holiday.mp4", "_ou", "_sbs") {
		t.Error("hasAny found an absent substring")
	}
	if hasAny("holiday.mp4") {
		t.Error("hasAny with no needles should be false")
	}
}

// TestDetectOnRealWorldNames is the regression test for two faults that only a
// real file exposed. Every case here comes from an actual path on disk, not from
// a tidy name invented to pass.
func TestDetectOnRealWorldNames(t *testing.T) {
	// The fault: "3600p" contains "360", and so does the date in "230308".
	// Both made a VR180 file read as a full sphere — which shows the world
	// squeezed into half the view, the exact failure the reasoning string exists
	// to make explicable.
	const realPath = "/Users/x/Movies/POVROriginals.23.03.08.Maddy.May.XXX.VR180.3600p.MP4-VACCiNE[XC]/vac-povro230308mm-3600.mp4"
	g := Detect(realPath, 7200, 3600)
	if g.Projection.HSpanDeg != 180 {
		t.Errorf("real VR180 path detected as %.0f degrees wide, want 180 (why: %s)", g.Projection.HSpanDeg, g.Why)
	}
	if g.Format.Layout != stereo.SideBySide {
		t.Errorf("real VR180 path detected as %v, want side-by-side (why: %s)", g.Format.Layout, g.Why)
	}

	// The marker often lives in the FOLDER, not the file: the file itself here
	// is just "vac-povro230308mm-3600.mp4". Detect is given the whole path for
	// exactly that reason.
	base := Detect("vac-povro230308mm-3600.mp4", 7200, 3600)
	if base.Projection.HSpanDeg == 180 {
		t.Error("the bare filename should NOT be enough to say 180; that would be luck, not detection")
	}
	if strings.Contains(base.Why, "says 360") {
		t.Errorf("a resolution of 3600 was read as a 360 marker: %s", base.Why)
	}
}

// TestHasNumberMarker pins the boundary rule that fixed the above: a run of
// digits inside a longer number is not a marker.
func TestHasNumberMarker(t *testing.T) {
	for _, tc := range []struct {
		s, marker string
		want      bool
	}{
		{"holiday_360_sbs.mp4", "360", true},
		{"clip-360.mp4", "360", true},
		{"360.mp4", "360", true},
		{"vr180", "180", true},
		{"dive_180_sbs", "180", true},
		// The real-world false positives.
		{"3600p", "360", false},
		{"povro230308mm-3600", "360", false},
		{"7200x3600", "360", false},
		{"1800p", "180", false},
		{"x1802", "180", false},
		// A digit BEFORE the marker disqualifies it too: 1360 is not 360.
		{"clip1360.mp4", "360", false},
		{"v2180p", "180", false},
		// Absent entirely.
		{"holiday.mp4", "360", false},
		{"", "360", false},
	} {
		if got := hasNumberMarker(tc.s, tc.marker); got != tc.want {
			t.Errorf("hasNumberMarker(%q, %q) = %v, want %v", tc.s, tc.marker, got, tc.want)
		}
	}
}

func TestIsDigit(t *testing.T) {
	for _, b := range []byte{'0', '5', '9'} {
		if !isDigit(b) {
			t.Errorf("isDigit(%q) = false", b)
		}
	}
	for _, b := range []byte{'a', '/', ':', 'z', '.'} {
		if isDigit(b) {
			t.Errorf("isDigit(%q) = true", b)
		}
	}
}

// TestVR180ImpliesStereo pins that the format name alone settles the packing,
// while a bare 180 does not: a monoscopic hemisphere is a real thing.
func TestVR180ImpliesStereo(t *testing.T) {
	if g := Detect("clip_vr180.mp4", 7200, 3600); g.Format.Layout != stereo.SideBySide {
		t.Errorf("vr180 gave %v, want side-by-side (why: %s)", g.Format.Layout, g.Why)
	}
	if g := Detect("clip_180.mp4", 3840, 1920); g.Format.Layout != stereo.Mono {
		t.Errorf("a bare 180 gave %v, want mono (why: %s)", g.Format.Layout, g.Why)
	}
	// An explicit packing still wins over the format name.
	if g := Detect("clip_vr180_ou.mp4", 3840, 3840); g.Format.Layout != stereo.OverUnder {
		t.Errorf("explicit over-under gave %v (why: %s)", g.Format.Layout, g.Why)
	}
}
