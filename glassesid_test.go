package player

import (
	"math"
	"testing"

	"github.com/go-xrkit/xrkit/glasses"
	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
)

// The XREAL 1S, which enumerates on the USB tree, and a VITURE microphone,
// which is what asking HID for VITURE's vendor actually returned while a pair
// of Luma Ultra glasses was attached. Both are real readings.
var (
	xreal1S      = glasses.USB{Vendor: 0x3318, Product: 0x043e, Name: "XREAL 1S"}
	vitureMicro  = glasses.USB{Vendor: 0x35ca, Product: 0x1102, Name: "VITURE Microphone"}
	vitureGlasse = glasses.USB{Vendor: 0x35ca, Product: 0x1104, Name: "VITURE Luma Ultra XR GLASSES"}
)

func TestIdentifyGlassesPrefersTheProductIdOverTheDisplayName(t *testing.T) {
	// Every VITURE panel reports the display name "VITURE", so the display name
	// can only ever name the brand. The product id names the model.
	byName, howName := identifyGlasses("VITURE", nil)
	if howName != glasses.ByDisplayName {
		t.Fatalf("a VITURE display was identified by %v, want the display name", howName)
	}
	if byName.Known() {
		t.Errorf("the display name alone produced a usable field of view for %q, which no VITURE panel can support", byName.Model)
	}
	byUSB, howUSB := identifyGlasses("VITURE", []glasses.USB{vitureMicro, vitureGlasse})
	if howUSB != glasses.ByUSBProduct {
		t.Fatalf("the glasses were identified by %v, want the USB product", howUSB)
	}
	if byUSB.Model == byName.Model {
		t.Errorf("the USB product named %q, the same as the display name did, so the stronger evidence bought nothing", byUSB.Model)
	}
	if !byUSB.Known() {
		t.Errorf("%q was identified but has no usable field of view", byUSB.Model)
	}
}

func TestIdentifyGlassesKeepsTheStrongestEvidence(t *testing.T) {
	// The order the devices come back in must not decide the answer: a machine
	// with several devices attached lists them in whatever order IOKit walked.
	forwards, howF := identifyGlasses("VITURE", []glasses.USB{vitureMicro, vitureGlasse, xreal1S})
	backwards, howB := identifyGlasses("VITURE", []glasses.USB{xreal1S, vitureGlasse, vitureMicro})
	if howF != howB || forwards.Model != backwards.Model {
		t.Errorf("device order changed the answer: %q by %v, then %q by %v",
			forwards.Model, howF, backwards.Model, howB)
	}
}

func TestIdentifyGlassesFallsBackToTheBrand(t *testing.T) {
	// This is the real reading taken with Luma Ultra glasses attached: the
	// only VITURE device that enumerated was the microphone, whose product id
	// names no model. A brand is the honest answer, not a guess at a model.
	p, how := identifyGlasses("VITURE", []glasses.USB{vitureMicro})
	if how == glasses.NotIdentified {
		t.Fatal("a VITURE display and a VITURE device identified nothing at all")
	}
	if p.Known() {
		t.Errorf("%q was given a usable field of view on brand evidence alone", p.Model)
	}
}

func TestIdentifyGlassesFindsNothingInNothing(t *testing.T) {
	p, how := identifyGlasses("DELL U2723QE", nil)
	if how != glasses.NotIdentified {
		t.Errorf("an ordinary monitor was identified as %q by %v", p.Model, how)
	}
}

func TestRankOrdersEvidenceFromNothingToAModel(t *testing.T) {
	// glasses.How is declared weakest-last, so comparing the values directly
	// gets the order backwards. That is the whole reason rank exists.
	order := []glasses.How{
		glasses.NotIdentified, glasses.ByUSBVendor, glasses.ByDisplayName, glasses.ByUSBProduct,
	}
	for i := 1; i < len(order); i++ {
		if rank(order[i]) <= rank(order[i-1]) {
			t.Errorf("%v does not outrank %v", order[i], order[i-1])
		}
	}
	if got := rank(glasses.How(99)); got != 0 {
		t.Errorf("an unknown How ranked %d, want nothing", got)
	}
}

func TestViewFOVTakesTheModelsOwnFigure(t *testing.T) {
	p, how := identifyGlasses("VITURE", []glasses.USB{vitureGlasse})
	if how != glasses.ByUSBProduct {
		t.Fatalf("identified by %v, want the USB product", how)
	}
	const eyeAspect = 1920.0 / 1200.0
	fovy, known := viewFOV(p, eyeAspect)
	if !known {
		t.Fatalf("%q has a published field of view but viewFOV would not use it", p.Model)
	}
	if fovy == fallbackFOVyDeg {
		t.Errorf("the model's own figure came out as the fallback %v, so nothing was looked up", fovy)
	}
	// A pair of glasses whose panel fills a modest part of the sight: far
	// narrower than the fallback, which is what made the old default wrong.
	if fovy < 15 || fovy > 45 {
		t.Errorf("%q has a %.1f degree vertical field, which no such headset has", p.Model, fovy)
	}
	// The same glasses on a differently shaped panel see a different vertical
	// angle, because a diagonal figure only becomes vertical through a shape.
	if wide, _ := viewFOV(p, 32.0/9.0); math.Abs(wide-fovy) < 0.5 {
		t.Errorf("the vertical field was %.1f on a 16:10 eye and %.1f on a 32:9 one, so the panel shape was ignored", fovy, wide)
	}
}

func TestViewFOVRefusesToGuess(t *testing.T) {
	brand, _ := identifyGlasses("VITURE", nil)
	fovy, known := viewFOV(brand, 1.6)
	if known {
		t.Errorf("a brand-only profile produced a field of view said to be known")
	}
	if fovy != fallbackFOVyDeg {
		t.Errorf("the fallback is %v, not %v", fallbackFOVyDeg, fovy)
	}
	if _, known := viewFOV(glasses.Profile{}, 1.6); known {
		t.Error("an empty profile produced a field of view said to be known")
	}
	// A positive control: the refusals above are about the profile, not about
	// viewFOV refusing everything.
	full, _ := identifyGlasses("", []glasses.USB{xreal1S})
	if _, known := viewFOV(full, 1.6); !known {
		t.Error("viewFOV would not use a fully identified model either, so the refusals prove nothing")
	}
}

func TestFitToViewFillsTheEyeWithAFlatFilm(t *testing.T) {
	// The reading this answers: a 2.40:1 film on a 1920x1200 eye covered 11.0%
	// of it, because the screen was a fixed 60 by 34 degrees.
	const eyeAspect = 1920.0 / 1200.0
	g := Geometry{Projection: projection.Screen}
	info := SourceInfo{Width: 1920, Height: 800}
	out, ok := fitToView(g, info, eyeAspect, 90)
	if !ok {
		t.Fatal("fitToView would not fit an ordinary flat film")
	}
	if out.Projection == g.Projection {
		t.Fatal("the screen came back unchanged")
	}
	// The screen now has the FILM's shape, not the fixed one's.
	film := 1920.0 / 800.0
	h := math.Tan(out.Projection.HSpanDeg * math.Pi / 360)
	v := math.Tan(out.Projection.VSpanDeg * math.Pi / 360)
	if got := h / v; math.Abs(got-film) > 1e-9 {
		t.Errorf("the screen is %.4f:1 for a %.4f:1 film", got, film)
	}
	// And it fills the eye across, which is where a film wider than the eye
	// runs out first.
	view := math.Tan(90 * math.Pi / 360)
	if across := h / (view * eyeAspect); math.Abs(across-1) > 1e-9 {
		t.Errorf("the screen spans %.4f of the eye's width, want all of it", across)
	}
	if out.Why == g.Why {
		t.Error("the geometry does not say it was resized")
	}
}

func TestFitToViewUsesOneEyeOfASideBySideFilm(t *testing.T) {
	// A side-by-side frame is twice as wide as what either eye is shown, so
	// fitting the FRAME would make the picture half as tall as it should be.
	const eyeAspect = 1920.0 / 1200.0
	info := SourceInfo{Width: 3840, Height: 800}
	flat := Geometry{Projection: projection.Screen}
	sbs := Geometry{Projection: projection.Screen}
	sbs.Format.Layout = stereo.SideBySide
	whole, ok1 := fitToView(flat, info, eyeAspect, 90)
	half, ok2 := fitToView(sbs, info, eyeAspect, 90)
	if !ok1 || !ok2 {
		t.Fatal("fitToView would not fit one of the two layouts")
	}
	if whole.Projection == half.Projection {
		t.Fatal("a side-by-side film was fitted as though it were one picture")
	}
	if half.Projection.VSpanDeg <= whole.Projection.VSpanDeg {
		t.Errorf("one eye of a side-by-side film came out %.1f degrees tall against %.1f for the whole frame, but one eye is the SQUARER picture and should be taller",
			half.Projection.VSpanDeg, whole.Projection.VSpanDeg)
	}
}

func TestFitToViewLeavesPanoramicGeometryAlone(t *testing.T) {
	info := SourceInfo{Width: 4096, Height: 2048}
	for _, p := range []projection.Projection{projection.Sphere360, projection.Hemisphere180, projection.Fisheye180} {
		g := Geometry{Projection: p}
		out, ok := fitToView(g, info, 1.6, 90)
		if ok {
			t.Errorf("%v was resized, but a sphere already surrounds the viewer", p.Kind)
		}
		if out.Projection != p {
			t.Errorf("%v came back changed", p.Kind)
		}
	}
}

func TestFitToViewRefusesAPictureWithNoSize(t *testing.T) {
	g := Geometry{Projection: projection.Screen}
	for _, c := range []struct {
		name string
		info SourceInfo
		fovy float64
	}{
		{"a source with no width", SourceInfo{Width: 0, Height: 800}, 90},
		{"a source with no height", SourceInfo{Width: 1920, Height: 0}, 90},
		{"no field of view", SourceInfo{Width: 1920, Height: 800}, 0},
		{"a field of a half turn", SourceInfo{Width: 1920, Height: 800}, 180},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, ok := fitToView(g, c.info, 1.6, c.fovy)
			if ok {
				t.Errorf("fitToView fitted %s", c.name)
			}
			if out.Projection != g.Projection {
				t.Error("it changed the geometry anyway")
			}
		})
	}
	if _, ok := fitToView(g, SourceInfo{Width: 1920, Height: 800}, 1.6, 90); !ok {
		t.Error("fitToView refused an ordinary film, so the refusals prove nothing")
	}
}

func TestIdentifyGlassesRefusesToChooseBetweenTwoHeadsets(t *testing.T) {
	// Both makers plugged in and a display that names neither: two devices name
	// two different models just as strongly, and nothing present separates
	// them. Naming one would be a coin toss dressed as a measurement.
	p, how := identifyGlasses("Built-in Retina Display", []glasses.USB{vitureGlasse, xreal1S})
	if how != glasses.NotIdentified {
		t.Errorf("with two headsets attached and no display to tell them apart, it chose %q by %v", p.Model, how)
	}
	// But when the display names one of the brands, the other is not about the
	// panel being drawn on, and there is no ambiguity left.
	onViture, howV := identifyGlasses("VITURE", []glasses.USB{vitureGlasse, xreal1S})
	if howV != glasses.ByUSBProduct || onViture.Vendor() != vitureGlasse.Vendor {
		t.Errorf("playing on the VITURE panel identified %q by %v", onViture.Model, howV)
	}
	onXreal, howX := identifyGlasses("XREAL 1S", []glasses.USB{vitureGlasse, xreal1S})
	if howX != glasses.ByUSBProduct || onXreal.Vendor() != xreal1S.Vendor {
		t.Errorf("playing on the XREAL panel identified %q by %v", onXreal.Model, howX)
	}
	if onViture.Model == onXreal.Model {
		t.Errorf("both panels identified the same model %q", onViture.Model)
	}
}
