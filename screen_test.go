package player

import (
	"strings"
	"testing"
)

var (
	laptop = Display{Name: "Built-in Retina Display", Width: 2056, Height: 1329, Primary: true}
	beast  = Display{Name: "VITURE Beast", Width: 3840, Height: 1080}
	cinema = Display{Name: "DELL U2723QE", Width: 3840, Height: 2160}
	small  = Display{Name: "HDMI Monitor", Width: 1920, Height: 1080}
)

func TestChooseDisplayPrefersRecognisedGlasses(t *testing.T) {
	// Even with a bigger external monitor attached, the glasses win.
	got, err := ChooseDisplay([]Display{laptop, cinema, beast}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != beast.Name {
		t.Errorf("chose %v, want the glasses", got)
	}
}

func TestChooseDisplayFallsBackToTheWidestExternal(t *testing.T) {
	got, err := ChooseDisplay([]Display{laptop, small, cinema}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != cinema.Name {
		t.Errorf("chose %v, want the widest non-primary", got)
	}
}

func TestChooseDisplayFallsBackToPrimary(t *testing.T) {
	got, err := ChooseDisplay([]Display{laptop}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != laptop.Name {
		t.Errorf("chose %v, want the primary", got)
	}
	// A list with no primary at all still yields something rather than failing.
	got, err = ChooseDisplay([]Display{{Name: "odd", Width: 800, Height: 600, Primary: false}}, "")
	if err != nil {
		t.Fatalf("a single non-primary display gave an error: %v", err)
	}
	if got.Name != "odd" {
		t.Errorf("chose %v", got)
	}
}

func TestChooseDisplayByName(t *testing.T) {
	got, err := ChooseDisplay([]Display{laptop, beast, cinema}, "dell")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != cinema.Name {
		t.Errorf("chose %v, want the Dell", got)
	}
	// Case must not matter.
	if got, err := ChooseDisplay([]Display{laptop, beast}, "VITURE"); err != nil || got.Name != beast.Name {
		t.Errorf("case-insensitive match gave (%v, %v)", got, err)
	}
}

// TestChooseDisplayRefusesAnAmbiguousRequest matters because guessing wrong here
// takes over a screen the user was working on.
func TestChooseDisplayRefusesAnAmbiguousRequest(t *testing.T) {
	two := []Display{
		{Name: "VITURE Beast", Width: 3840, Height: 1080},
		{Name: "VITURE Luma", Width: 1920, Height: 1080},
	}
	_, err := ChooseDisplay(two, "viture")
	if err == nil {
		t.Fatal("an ambiguous name was resolved silently")
	}
	if !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("error %q should say how many matched", err)
	}
	if !strings.Contains(err.Error(), "Luma") {
		t.Errorf("error %q should list the candidates", err)
	}
}

func TestChooseDisplayErrors(t *testing.T) {
	if _, err := ChooseDisplay(nil, ""); err == nil {
		t.Error("no displays should be an error")
	}
	_, err := ChooseDisplay([]Display{laptop}, "nonexistent")
	if err == nil {
		t.Fatal("an unmatched name should be an error")
	}
	if !strings.Contains(err.Error(), "Built-in") {
		t.Errorf("error %q should list what IS attached", err)
	}
}

func TestDisplayString(t *testing.T) {
	if s := laptop.String(); !strings.Contains(s, "primary") || !strings.Contains(s, "2056x1329") {
		t.Errorf("String() = %q", s)
	}
	if s := beast.String(); strings.Contains(s, "primary") {
		t.Errorf("String() = %q, should not claim primary", s)
	}
}

// TestStereoMode pins the fact the whole stereo path rests on: the glasses
// expose 3D as a DISPLAY MODE, so its detection is arithmetic on the panel size
// and needs no SDK.
func TestStereoMode(t *testing.T) {
	for _, tc := range []struct {
		name               string
		w, h               int
		wantStereo         bool
		wantEyeW, wantEyeH int
	}{
		{"Beast in SBS 3D mode", 3840, 1080, true, 1920, 1080},
		{"Beast in 2D mode", 1920, 1200, false, 1920, 1200},
		// 32:9 reads as two eyes. A genuine 32:9 monitor would too -- that blind
		// spot is documented, not hidden, because no arithmetic can separate them.
		{"32:9 reads as two eyes", 5120, 1440, true, 2560, 1440},
		{"a 21:9 ultrawide is one eye", 3440, 1440, false, 3440, 1440},
		{"a 21:9 at 2560x1080 is one eye", 2560, 1080, false, 2560, 1080},
		{"an ordinary 16:9 panel", 1920, 1080, false, 1920, 1080},
		{"a 4K 16:9 panel", 3840, 2160, false, 3840, 2160},
		{"nothing", 0, 0, false, 0, 0},
		{"negative", -1, -1, false, 0, 0},
	} {
		s, ew, eh := StereoMode(tc.w, tc.h)
		if s != tc.wantStereo || ew != tc.wantEyeW || eh != tc.wantEyeH {
			t.Errorf("%s: StereoMode(%d,%d) = (%v,%d,%d), want (%v,%d,%d)",
				tc.name, tc.w, tc.h, s, ew, eh, tc.wantStereo, tc.wantEyeW, tc.wantEyeH)
		}
	}
}

// TestChooseDisplayLastResort covers the final fallback: a list where nothing is
// primary and nothing has a usable width. It should still answer rather than
// fail, because refusing to play because the display list is odd is worse than
// playing on the only thing there is.
func TestChooseDisplayLastResort(t *testing.T) {
	odd := []Display{{Name: "unknown", Width: 0, Height: 0}}
	got, err := ChooseDisplay(odd, "")
	if err != nil {
		t.Fatalf("ChooseDisplay = %v, want the sole display", err)
	}
	if got.Name != "unknown" {
		t.Errorf("chose %v, want the sole display", got)
	}
}

// TestScalingAdvice covers the warning for a display rendering more pixels than
// its panel can show. It exists because a VITURE Beast was observed reporting
// "5120x1600 rendered, looks like 2560x800" while its panel is 3840x1080 —
// three quarters of the rendered pixels thrown away, with nothing to say so.
func TestScalingAdvice(t *testing.T) {
	scaledBeast := Display{Name: "VITURE Beast", Width: 2560, Height: 800, Scale: 2}
	got := ScalingAdvice(scaledBeast)
	if got == "" {
		t.Fatal("a 2x-scaled pair of glasses produced no advice")
	}
	for _, want := range []string{"SCALED", "2560x800", "5120x1600", "VITURE Beast"} {
		if !strings.Contains(got, want) {
			t.Errorf("advice %q should mention %q", got, want)
		}
	}

	// Nothing to say about a pixel-exact mode.
	if got := ScalingAdvice(Display{Name: "VITURE Beast", Width: 3840, Height: 1080, Scale: 1}); got != "" {
		t.Errorf("a 1x mode produced advice: %q", got)
	}
	// A zero scale means the back-end did not report one; say nothing rather
	// than accuse the display of something on missing information.
	if got := ScalingAdvice(Display{Name: "VITURE Beast", Width: 3840, Height: 1080, Scale: 0}); got != "" {
		t.Errorf("an unreported scale produced advice: %q", got)
	}
	// A scaled ORDINARY display is the user's own choice, not our business.
	if got := ScalingAdvice(Display{Name: "Built-in Retina Display", Width: 2056, Height: 1329, Scale: 2, Primary: true}); got != "" {
		t.Errorf("a scaled laptop panel produced advice: %q", got)
	}
	if got := ScalingAdvice(Display{Name: "DELL U2723QE", Width: 2560, Height: 1440, Scale: 2}); got != "" {
		t.Errorf("a scaled external monitor produced advice: %q", got)
	}
}

func TestKnownGlassesName(t *testing.T) {
	for _, n := range []string{"VITURE Beast", "viture luma", "XREAL One Pro", "Rokid Max", "TCL NXTWEAR S"} {
		if !knownGlassesName(n) {
			t.Errorf("%q was not recognised as glasses", n)
		}
	}
	for _, n := range []string{"Built-in Retina Display", "DELL U2723QE", "", "HDMI Monitor"} {
		if knownGlassesName(n) {
			t.Errorf("%q was wrongly recognised as glasses", n)
		}
	}
}
