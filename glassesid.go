package player

import (
	"github.com/go-xrkit/xrkit/glasses"
	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
)

// fallbackFOVyDeg is the vertical field of view assumed when the glasses are
// not identified, or are identified only as a brand.
//
// It is a FRAMING and not a measurement, and the difference matters. Filling
// the view does not depend on it -- the screen is sized to the view, so the
// picture covers the same pixels whatever this is -- so a wrong number here
// cannot shrink the picture the way the old fixed screen did. What it does
// affect is the angle the player REPORTS, which is why an unidentified model
// is said to be unidentified rather than quietly given a number.
const fallbackFOVyDeg = 90

// identifyGlasses resolves which model is attached from everything to hand:
// every USB device on the machine, and the display's name.
//
// It tries each device rather than picking one first, because the strongest
// evidence is a USB PRODUCT id and only the catalogue knows which ids belong to
// glasses. A VITURE panel reports the display name "VITURE" whatever the model
// is, so the display name alone can name a brand and never a model, which is
// exactly the difference between a real field of view and a guess.
func identifyGlasses(displayName string, devices []glasses.USB) (glasses.Profile, glasses.How) {
	best, bestHow := glasses.IdentifyDevice(displayName, nil)
	// When the display names a brand, only that brand's devices are about the
	// panel being drawn on. Both makers' glasses plugged in at once is an
	// ordinary state on this machine, and without this the answer came from
	// whichever device the enumeration listed first.
	brand := best.Vendor()
	for i := range devices {
		if brand != 0 && devices[i].Vendor != brand {
			continue
		}
		p, how := glasses.IdentifyDevice(displayName, &devices[i])
		switch {
		case rank(how) > rank(bestHow):
			best, bestHow = p, how
		case rank(how) == rank(bestHow) && p.Model != best.Model:
			// Two devices name DIFFERENT models just as strongly and nothing
			// present can separate them. Picking one would be a coin toss
			// wearing the clothes of a measurement, so neither is taken.
			return glasses.Profile{}, glasses.NotIdentified
		}
	}
	return best, bestHow
}

// rank orders evidence from nothing to a named model. glasses.How is declared
// weakest-last, so it cannot be compared directly.
func rank(h glasses.How) int {
	switch h {
	case glasses.ByUSBProduct:
		return 3
	case glasses.ByDisplayName:
		return 2
	case glasses.ByUSBVendor:
		return 1
	}
	return 0
}

// viewFOV is the vertical field of view of one eye: the identified model's own
// published figure, converted to the vertical angle for the panel shape it is
// being shown on.
//
// known is false when the model is not known well enough to have one, in which
// case [fallbackFOVyDeg] is returned. The catalogue refuses to guess -- a family
// entry, or a figure published without saying which angle it spans, both come
// back unusable -- and that refusal is passed on rather than papered over.
func viewFOV(p glasses.Profile, eyeAspect float64) (fovyDeg float64, known bool) {
	if _, v, ok := p.FOV(eyeAspect); ok {
		return v, true
	}
	return fallbackFOVyDeg, false
}

// fitToView resizes a flat virtual screen to fill the eye.
//
// Detect gives flat material projection.Screen, a fixed 60 by 34 degrees --
// "roughly what a large television occupies from the sofa". That is the wrong
// shape for any picture that is not 1.889:1, and the wrong size for any view
// that is not the one it silently assumed: a film measured through the glasses
// covered 11.0% of the eye. Sizing the screen to the view instead fixes both,
// and needs nothing to be known about the glasses.
//
// Non-flat geometry is returned unchanged and ok is false. A sphere already
// surrounds the viewer, so there is no screen to fit and the field of view is
// doing its proper job of framing.
func fitToView(g Geometry, info SourceInfo, eyeAspect, fovyDeg float64) (out Geometry, ok bool) {
	if g.Projection.Kind != projection.Flat {
		return g, false
	}
	// The picture is ONE EYE of the source, not the frame: a side-by-side film
	// is twice as wide as what either eye is shown.
	eye := g.Format.EyeRect(stereo.Left, info.Width, info.Height)
	if eye.W <= 0 || eye.H <= 0 {
		return g, false
	}
	p, ok := projection.FillScreen(fovyDeg, eyeAspect, float64(eye.W)/float64(eye.H))
	if !ok {
		return g, false
	}
	g.Projection = p
	g.Why += "; screen sized to fill the view"
	return g, true
}
