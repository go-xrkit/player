package player

import (
	"testing"

	"github.com/go-macos/coreaudio"
)

// The devices this machine really reports, with both headsets attached. The
// microphones matter: glasses publish one, and it answers to the same name as
// the output.
var attached = []coreaudio.Device{
	{UID: "0ED43331-0000-0000-2021-010380100A78", Name: "VITURE", Outputs: 1},
	{UID: "4C2D7674-0000-0000-1723-0104B58C2878", Name: "Odyssey G95NC", Outputs: 1},
	{UID: "AppleUSBAudioEngine:VITURE.:VITURE Microphone:1234333333:1", Name: "VITURE Microphone", Inputs: 1},
	{UID: "AppleUSBAudioEngine:XREAL:XREAL 1S:1100000:6", Name: "XREAL 1S", Outputs: 1},
	{UID: "AppleUSBAudioEngine:XREAL:XREAL 1S:1100000:7", Name: "XREAL 1S", Inputs: 1},
	{UID: "BuiltInSpeakerDevice", Name: "MacBook Pro Speakers", Outputs: 1},
	{UID: "BuiltInMicrophoneDevice", Name: "MacBook Pro Microphone", Inputs: 1},
}

func TestPickAudioFollowsTheDisplay(t *testing.T) {
	cases := []struct {
		display string
		want    string // the UID expected
	}{
		{"VITURE", "0ED43331-0000-0000-2021-010380100A78"},
		{"XREAL 1S", "AppleUSBAudioEngine:XREAL:XREAL 1S:1100000:6"},
		{"Odyssey G95NC", "4C2D7674-0000-0000-1723-0104B58C2878"},
	}
	for _, c := range cases {
		t.Run(c.display, func(t *testing.T) {
			d, ok := pickAudio(attached, c.display, "")
			if !ok {
				t.Fatalf("no output found for the %q panel", c.display)
			}
			if d.UID != c.want {
				t.Errorf("sound would go to %v, want the device with id %q", d, c.want)
			}
			if !d.CanPlay() {
				t.Errorf("sound would go to %v, which cannot play", d)
			}
		})
	}
}

// TestPickAudioIgnoresTheMicrophone: a headset publishes a microphone under
// almost the same name, and it is not somewhere to put sound.
func TestPickAudioIgnoresTheMicrophone(t *testing.T) {
	only := []coreaudio.Device{
		{UID: "mic", Name: "VITURE Microphone", Inputs: 1},
	}
	if d, ok := pickAudio(only, "VITURE", ""); ok {
		t.Errorf("sound would go to %v, which is a microphone", d)
	}
	// The positive control: add the output and it is found, so the refusal
	// above is about the direction and not about the name.
	with := append(only, coreaudio.Device{UID: "out", Name: "VITURE", Outputs: 1})
	if _, ok := pickAudio(with, "VITURE", ""); !ok {
		t.Error("the output was not found either, so the refusal proves nothing")
	}
}

func TestPickAudioLeavesAnOrdinaryScreenToTheSystem(t *testing.T) {
	// A laptop panel has no audio device of its own, and picking one on a weak
	// resemblance would be worse than the default -- the default is at least
	// what the person set.
	if d, ok := pickAudio(attached, "Built-in Retina Display", ""); ok {
		t.Errorf("a display with no audio was given %v", d)
	}
	if d, ok := pickAudio(attached, "", ""); ok {
		t.Errorf("no display at all was given %v", d)
	}
	if d, ok := pickAudio(attached, "   ", ""); ok {
		t.Errorf("a blank display name was given %v", d)
	}
	if d, ok := pickAudio(nil, "VITURE", ""); ok {
		t.Errorf("a machine with no audio devices was given %v", d)
	}
}

func TestPickAudioTakesWhatWasAskedFor(t *testing.T) {
	// By unique id, which is unambiguous by construction.
	d, ok := pickAudio(attached, "VITURE", "BuiltInSpeakerDevice")
	if !ok || d.Name != "MacBook Pro Speakers" {
		t.Errorf("asking for the speakers by id gave %v (found=%v)", d, ok)
	}
	// By name, case and spacing aside.
	d, ok = pickAudio(attached, "VITURE", "  macbook pro speakers ")
	if !ok || d.UID != "BuiltInSpeakerDevice" {
		t.Errorf("asking for the speakers by name gave %v (found=%v)", d, ok)
	}
	// A device that is not there is not quietly replaced by the display's own.
	if d, ok := pickAudio(attached, "VITURE", "Sonos Beam"); ok {
		t.Errorf("asking for an absent device gave %v", d)
	}
	// Nor by a microphone that happens to carry the name.
	if d, ok := pickAudio(attached, "VITURE", "VITURE Microphone"); ok {
		t.Errorf("asking for a microphone gave %v", d)
	}
}

// TestPickAudioRefusesToChooseBetweenTwoOfTheSameName: two devices answering to
// one name cannot be told apart by name, and taking the first would be a coin
// toss reported as a choice.
func TestPickAudioRefusesToChooseBetweenTwoOfTheSameName(t *testing.T) {
	twins := []coreaudio.Device{
		{UID: "left", Name: "VITURE", Outputs: 1},
		{UID: "right", Name: "VITURE", Outputs: 1},
	}
	if d, ok := pickAudio(twins, "VITURE", ""); ok {
		t.Errorf("one of two identically named outputs was chosen: %v", d)
	}
	// Naming one by its id settles it, which is what ids are for.
	if d, ok := pickAudio(twins, "VITURE", "right"); !ok || d.UID != "right" {
		t.Errorf("asking for one by id gave %v (found=%v)", d, ok)
	}
}

// TestPickAudioMatchesAPartialName: a panel and its audio device do not always
// spell themselves identically, so one containing the other counts -- but only
// when exactly one does.
func TestPickAudioMatchesAPartialName(t *testing.T) {
	devs := []coreaudio.Device{
		{UID: "beast", Name: "VITURE", Outputs: 1},
		{UID: "speakers", Name: "MacBook Pro Speakers", Outputs: 1},
	}
	d, ok := pickAudio(devs, "VITURE Beast XR Glasses", "")
	if !ok || d.UID != "beast" {
		t.Errorf("a longer panel name gave %v (found=%v)", d, ok)
	}
	// Several partial matches and no exact one is still ambiguous.
	many := append(devs, coreaudio.Device{UID: "other", Name: "VITURE Beast", Outputs: 1})
	if d, ok := pickAudio(many, "VITURE Beast XR Glasses", ""); ok {
		t.Errorf("two partial matches gave %v", d)
	}
	// A device with no name at all matches nothing, rather than everything.
	blank := []coreaudio.Device{{UID: "nameless", Outputs: 1}}
	if d, ok := pickAudio(blank, "VITURE", ""); ok {
		t.Errorf("an unnamed device was matched to a named panel: %v", d)
	}
}
