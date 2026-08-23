package player

import (
	"fmt"
	"strings"
)

// Display is the subset of a window back-end's screen description this package
// needs. It is declared here, rather than taken from go-widgets/window, so the
// choosing logic can be tested against a list of displays that do not exist.
type Display struct {
	Name          string
	Width, Height int
	Primary       bool
	// Scale is the display's backing factor: framebuffer pixels per logical
	// point. 1 means the framebuffer matches the mode; 2 means macOS renders at
	// twice the size and scales down.
	Scale float64
}

// String renders the display as a person would identify it.
func (d Display) String() string {
	s := fmt.Sprintf("%q %dx%d", d.Name, d.Width, d.Height)
	if d.Primary {
		s += " (primary)"
	}
	return s
}

// knownGlasses are substrings of the display names XR glasses publish. The list
// is a convenience, not a gate: an unrecognised headset still gets chosen by the
// fallback below, and can always be named explicitly.
var knownGlasses = []string{
	"viture", "xreal", "nreal", "rokid", "inmo", "even realities",
	"tcl nxtwear", "rayneo", "brilliant", "vitrue",
}

// ChooseDisplay picks the display to play on.
//
// want, when not empty, is matched case-insensitively against the display names
// and must match exactly one — an ambiguous request is an error rather than a
// coin toss, because playing full screen on the wrong monitor takes over the
// machine the user was using.
//
// With no preference the order is: a recognised pair of glasses; failing that
// the widest non-primary display, since an external panel is far more likely to
// be the intended target than the laptop the user is driving from; failing that
// the primary, so the thing still runs on one screen.
func ChooseDisplay(displays []Display, want string) (Display, error) {
	if len(displays) == 0 {
		return Display{}, fmt.Errorf("player: no displays attached")
	}
	if want != "" {
		var hits []Display
		for _, d := range displays {
			if strings.Contains(strings.ToLower(d.Name), strings.ToLower(want)) {
				hits = append(hits, d)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
			return Display{}, fmt.Errorf("player: no display matches %q; attached: %s",
				want, describe(displays))
		default:
			return Display{}, fmt.Errorf("player: %q matches %d displays: %s",
				want, len(hits), describe(hits))
		}
	}
	for _, d := range displays {
		for _, g := range knownGlasses {
			if strings.Contains(strings.ToLower(d.Name), g) {
				return d, nil
			}
		}
	}
	best := Display{}
	for _, d := range displays {
		if !d.Primary && d.Width > best.Width {
			best = d
		}
	}
	if best.Width > 0 {
		return best, nil
	}
	for _, d := range displays {
		if d.Primary {
			return d, nil
		}
	}
	return displays[0], nil
}

// describe lists displays for an error message.
func describe(displays []Display) string {
	parts := make([]string, len(displays))
	for i, d := range displays {
		parts[i] = d.String()
	}
	return strings.Join(parts, ", ")
}

// knownGlassesName reports whether a display name looks like XR glasses. It is
// the same list ChooseDisplay uses, factored out so the scaling advice can ask
// the question too.
func knownGlassesName(name string) bool {
	lower := strings.ToLower(name)
	for _, g := range knownGlasses {
		if strings.Contains(lower, g) {
			return true
		}
	}
	return false
}

// ScalingAdvice returns a warning when a display is in a SCALED mode, or "" when
// there is nothing to say.
//
// macOS offers displays a choice of modes, and a scaled one renders at a larger
// framebuffer and downsamples onto the panel. On a laptop screen that is a
// deliberate legibility trade. On XR glasses it is loss for nothing: the panel
// has one fixed native resolution, nobody is reading small text on it, and every
// downsample softens the image the user came for. A VITURE Beast was observed
// reporting "5120x1600 rendered, looks like 2560x800" while its panel is
// 3840x1080 -- three quarters of the rendered pixels thrown away.
//
// The advice is only offered for displays that look like glasses. An external
// monitor in a scaled mode is the user's business.
func ScalingAdvice(d Display) string {
	if d.Scale <= 1 || !knownGlassesName(d.Name) {
		return ""
	}
	return fmt.Sprintf(
		"%s is in a SCALED mode: %dx%d at %gx, so %dx%d is rendered and downsampled onto the panel. "+
			"Choose the panel's native mode in Displays settings for a sharper picture.",
		d.Name, d.Width, d.Height, d.Scale,
		int(float64(d.Width)*d.Scale), int(float64(d.Height)*d.Scale))
}

// StereoMode reports whether a display's dimensions look like a side-by-side 3D
// mode, and what one eye's viewport is.
//
// XR glasses expose their 3D mode AS A DISPLAY MODE: the VITURE Beast reports
// 3840x1080 for side-by-side 3D and 1920x1200 for ordinary 2D. So there is no
// SDK involved in getting stereo out — only the question of which mode the
// glasses are in, which is what this answers. A panel wider than 21:9 is
// carrying two eyes; anything else is one.
func StereoMode(w, h int) (stereoscopic bool, eyeW, eyeH int) {
	if w <= 0 || h <= 0 {
		return false, 0, 0
	}
	// The threshold sits at 3.0, between a 21:9 ultrawide (2.39 for the common
	// 3440x1440) and two 16:9 eyes side by side (3.56).
	//
	// It is a heuristic and it has a known blind spot: a genuine 32:9 monitor --
	// a Samsung Odyssey G9 is 5120x1440, also 3.56 -- reads as stereoscopic. No
	// arithmetic on the panel size can separate those two, because they are the
	// same panel size. So this is a DEFAULT, and the caller can say otherwise.
	if float64(w)/float64(h) > 3.0 {
		return true, w / 2, h
	}
	return false, w, h
}
