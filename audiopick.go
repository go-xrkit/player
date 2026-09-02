package player

import (
	"strings"

	"github.com/go-macos/coreaudio"
)

// pickAudio chooses the output device to play the sound on, given the display
// the picture is going to.
//
// Glasses publish their sound as an audio device named the way their panel is
// named -- a VITURE panel and a "VITURE" output, an XREAL 1S panel and an
// "XREAL 1S" output -- so the display being drawn on is the thing to match.
// Without this the sound goes to whatever the system defaults to, which on this
// machine is the Mac's own speakers: no error, no warning, and the picture in
// one place with its sound in another.
//
// want, when not empty, is what the user asked for by name or by unique id, and
// is matched instead of the display. An asked-for device that is not there is
// NOT quietly replaced by a guess: found is false, and the caller can say so.
//
// found is false when nothing matches, which is the ordinary case for a monitor
// that has no speakers of its own. The caller should then leave the system to
// choose, because a device picked on a weak resemblance is worse than the
// default -- the default is at least the one the person set.
func pickAudio(devs []coreaudio.Device, displayName, want string) (dev coreaudio.Device, found bool) {
	target := displayName
	if want != "" {
		target = want
	}
	if strings.TrimSpace(target) == "" {
		return coreaudio.Device{}, false
	}
	lower := strings.ToLower(strings.TrimSpace(target))

	var exact, partial []coreaudio.Device
	for _, d := range devs {
		if !d.CanPlay() {
			// A microphone by the right name is not somewhere to play sound,
			// and glasses publish one of those too.
			continue
		}
		if want != "" && d.UID == want {
			// A unique id is unambiguous by construction, so it ends the search.
			return d, true
		}
		name := strings.ToLower(strings.TrimSpace(d.Name))
		switch {
		case name == lower:
			exact = append(exact, d)
		case want == "" && name != "" && (strings.Contains(lower, name) || strings.Contains(name, lower)):
			// Resemblance is for INFERRING from a panel name, never for a
			// device a person named. Asking for one thing and being given a
			// near-namesake is the silent substitution this whole function
			// exists to avoid -- and a named device that cannot play is a
			// mistake worth being told about, not worth working around.
			partial = append(partial, d)
		}
	}
	if len(exact) == 1 {
		return exact[0], true
	}
	if len(exact) == 0 && len(partial) == 1 {
		return partial[0], true
	}
	// None, or several. Several is the interesting case: two devices answering
	// to one name cannot be told apart by name, and picking the first would be
	// a coin toss reported as a choice.
	return coreaudio.Device{}, false
}
