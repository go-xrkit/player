// Package player plays immersive video on XR glasses.
//
// It joins three things that know nothing about each other: a decoder
// (go-macos/avfoundation), the geometry of immersive video
// (go-xrkit/xrkit), and a window on the right physical display
// (go-widgets/window). What is left over — and what lives here — is the
// judgement: which display is the glasses, what shape the content is, and when
// to show each frame.
package player

import (
	"fmt"
	"math"
	"strings"

	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
)

// Geometry is everything needed to reshape a frame: what the source depicts, and
// how it packs its eyes.
type Geometry struct {
	Projection projection.Projection
	Format     stereo.Format
	// Why records how the guess was made, so a wrong one can be understood and
	// overridden rather than just disbelieved.
	Why string
}

// String renders the geometry compactly.
func (g Geometry) String() string {
	return fmt.Sprintf("%s %.0fx%.0f, %s eyes%s",
		g.Projection.Kind, g.Projection.HSpanDeg, g.Projection.VSpanDeg,
		g.Format.Layout, map[bool]string{true: " (swapped)"}[g.Format.Swapped])
}

// Detect guesses a file's geometry from its name and frame dimensions.
//
// Guessing is unavoidable: none of this is reliably recorded in the container,
// and the conventions that exist are conventions of FILE NAMING. The guess is
// therefore explicit about its reasoning, and every part of it is overridable —
// a viewer that silently mis-detects 180-degree content as 360 shows the world
// squeezed into half the view and gives the user nothing to act on.
//
// The order matters: an explicit marker in the name always beats a deduction
// from the aspect ratio, because a name was written by someone who knew.
func Detect(name string, w, h int) Geometry {
	lower := strings.ToLower(name)
	g := Geometry{}

	// --- eye packing ------------------------------------------------------
	switch {
	case hasAny(lower, "_ou", "-ou", "overunder", "over-under", "tb.", "_tb", "-tb"):
		g.Format.Layout = stereo.OverUnder
		g.Why = "name says over-under"
	case hasAny(lower, "_sbs", "-sbs", "sidebyside", "side-by-side", "_3d", "-3d", "half-sbs"):
		g.Format.Layout = stereo.SideBySide
		g.Why = "name says side-by-side"
	case hasAny(lower, "_mono", "-mono", "monoscopic"):
		g.Format.Layout = stereo.Mono
		g.Why = "name says monoscopic"
	default:
		// A 4:1 frame is two 2:1 images side by side -- a FULL SPHERE per eye,
		// which is stereoscopic 360, not VR180. VR180 stores 180x180 per eye,
		// which is SQUARE, so a side-by-side VR180 frame is 2:1 overall and
		// indistinguishable by aspect from a monoscopic sphere. That ambiguity is
		// real and is why the name is consulted first.
		if w > 0 && h > 0 && nearAspect(w, h, 4, 1) {
			g.Format.Layout = stereo.SideBySide
			g.Why = "4:1 frame: two 2:1 images side by side"
		} else if w > 0 && h > 0 && nearAspect(w, h, 1, 1) {
			g.Format.Layout = stereo.OverUnder
			g.Why = "1:1 frame: two 2:1 images stacked"
		} else if hasAny(lower, "vr180") {
			// VR180 is stereoscopic BY DEFINITION -- the specification has two
			// eyes -- so the format name breaks the tie where the aspect cannot:
			// side-by-side VR180 is 2:1 overall, indistinguishable from a
			// monoscopic sphere. It is consulted only HERE, after an explicit
			// marker and after a decisive aspect, because both of those know
			// more than a format name does.
			g.Format.Layout = stereo.SideBySide
			g.Why = "name says VR180, which is stereoscopic by definition"
		} else {
			g.Format.Layout = stereo.Mono
			g.Why = "no stereo marker in the name and no stereo aspect"
		}
	}

	if hasAny(lower, "_rl", "-rl", "righteye-first", "swapped") {
		g.Format.Swapped = true
		g.Why += "; name says the eyes are swapped"
	}

	// --- what the pixels depict -------------------------------------------
	// Measure the aspect of ONE EYE, not the frame: a side-by-side sphere and a
	// monoscopic sphere depict the same thing and have different frame shapes.
	eye := g.Format.EyeRect(stereo.Left, w, h)
	switch {
	case hasAny(lower, "fisheye", "_fish", "-fish"):
		g.Projection = projection.Fisheye180
		g.Why += "; name says fisheye"
	case hasAny(lower, "vr180", "hemisphere") || hasNumberMarker(lower, "180"):
		g.Projection = projection.Hemisphere180
		g.Why += "; name says 180"
	case hasAny(lower, "_sphere", "-sphere", "equirect") || hasNumberMarker(lower, "360"):
		g.Projection = projection.Sphere360
		g.Why += "; name says 360"
	case eye.W > 0 && eye.H > 0 && nearAspect(eye.W, eye.H, 2, 1):
		// A 2:1 eye is the equirectangular signature, and by convention a bare
		// 2:1 is a full sphere.
		g.Projection = projection.Sphere360
		g.Why += "; 2:1 eye, so equirectangular 360"
	case eye.W > 0 && eye.H > 0 && nearAspect(eye.W, eye.H, 1, 1):
		// A square eye holding 180x180 is how VR180 equirectangular is stored.
		g.Projection = projection.Hemisphere180
		g.Why += "; square eye, so 180x180"
	default:
		g.Projection = projection.Screen
		g.Why += "; ordinary aspect, so a flat screen"
	}
	return g
}

// hasNumberMarker reports whether s contains marker as a NUMBER rather than as
// a run of digits inside a longer one.
//
// A plain substring test is far too loose for a bare number, and real filenames
// prove it: "3600p" contains "360", and so does the date in
// "POVROriginals.23.03.08...3600p". Both made a VR180 file read as a full
// sphere, which shows the world squeezed into half the view. So a digit on
// either side disqualifies a match -- "vr180" and "holiday_360_sbs" still
// match, "3600p" and "230308" do not.
func hasNumberMarker(s, marker string) bool {
	for i := 0; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] != marker {
			continue
		}
		if i > 0 && isDigit(s[i-1]) {
			continue
		}
		if j := i + len(marker); j < len(s) && isDigit(s[j]) {
			continue
		}
		return true
	}
	return false
}

// isDigit reports whether b is an ASCII digit.
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// hasAny reports whether s contains any of the substrings.
func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// aspectTolerance is how far from an exact ratio still counts. Real files are
// cropped and padded by a few pixels, and 3840x1920 and 3840x1918 mean the same
// thing; 2% is loose enough for that and tight enough that 16:9 is never
// mistaken for 2:1.
const aspectTolerance = 0.02

// nearAspect reports whether w:h is within [aspectTolerance] of num:den.
func nearAspect(w, h, num, den int) bool {
	if w <= 0 || h <= 0 || den == 0 {
		return false
	}
	got := float64(w) / float64(h)
	want := float64(num) / float64(den)
	return math.Abs(got-want)/want <= aspectTolerance
}
