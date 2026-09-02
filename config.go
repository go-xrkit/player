package player

import (
	"fmt"
	"time"
)

// The configuration lives in a PORTABLE file, and its own file.
//
// It used to be declared twice -- once for darwin and once as a stub for
// everywhere else -- and the two had to be kept in step by hand. They were
// not: adding the 2D-to-3D fields to one broke the build of every other
// platform, which is the only reason anybody found out.

// Config parametrises [Play].
type Config struct {
	// Path is the video file.
	Path string
	// Screen names the display to play on, matched case-insensitively against
	// the attached displays. Empty picks automatically — recognised glasses
	// first. See [ChooseDisplay].
	Screen string
	// Geometry overrides what the content is. Nil detects it from the file name
	// and frame shape; see [Detect].
	Geometry *Geometry
	// FOVyDeg overrides the vertical field of view of each eye's view, in
	// degrees. Zero -- the usual case -- takes the field the IDENTIFIED model
	// publishes, per pair of glasses rather than per brand, and says so.
	//
	// It does not change how large flat material appears: the virtual screen
	// is sized to fill the view, so the picture covers the same pixels
	// whatever the field is. What it changes is the angle the player reports,
	// and the framing of panoramic material, which a field of view really
	// does frame.
	FOVyDeg float64
	// AudioDevice names the audio output to play the sound on, by its name or
	// by its unique id. Empty -- the usual case -- plays on the glasses' own
	// output when they publish one, and leaves the choice to the system when
	// they do not.
	//
	// A device asked for by name and not found is NOT replaced by a guess: the
	// player says so and lets the system choose, because silently playing
	// somewhere else is the fault this setting exists to fix.
	AudioDevice string
	// Loop restarts at the end instead of stopping.
	Loop bool
	// Mono forces a single-eye view even on a display that looks stereoscopic.
	// Useful for looking at the result on an ordinary monitor.
	Mono bool
	// Log, when set, receives one line of progress per notable event.
	Log func(string)
	// For, when positive, stops playback after this long. A full-screen window
	// with no chrome is awkward to end from the outside, and an automated check
	// cannot press a key at all.
	For time.Duration
	// Snapshot, when set, names a PNG to write once Frames have been shown. It
	// captures the composed framebuffer -- the exact pixels sent to the glasses --
	// which is the one form of visual proof that needs no screen-recording
	// consent.
	Snapshot string
	// SnapshotAfter is how many frames to show before taking the snapshot.
	// Zero means the first frame.
	SnapshotAfter int
	// Convert turns an ordinary FLAT film into 3D, frame by frame, by
	// estimating how far away everything is and synthesising a second eye.
	//
	// It is ignored for a film that is already stereoscopic, and on a display
	// showing one eye -- in both cases there is nothing to convert TO, and
	// saying so beats doing expensive work that changes nothing.
	//
	// What it cannot do is invent what the camera never saw. Where a near
	// object moves aside, what is behind it is guessed from the same row, so
	// an edge is a little smeared. That is the honest cost of the effect.
	Convert bool
	// DepthModel names a Core ML depth model -- an .mlpackage or an already
	// compiled .mlmodelc -- to estimate depth with. Empty falls back to an
	// estimate made from cues in the picture itself, which needs nothing at
	// all and is visibly not as good.
	DepthModel string
	// DepthCurve reshapes the depth before it becomes a sideways shift, as an
	// S-curve of the given strength. Zero means none.
	//
	// It flattens both ends of the depth range and expands the middle, so the
	// subject gains relief while the background and the foreground each settle
	// into their own plane. It is NOT a comfort control: a near object gets
	// slightly MORE disparity, not less.
	//
	// At the default disparity it barely matters -- twelve pixels each way is
	// thirteen distinct shifts for the whole range, and quantisation swamps
	// any reshaping of it. Raise Disparity before expecting to see this.
	DepthCurve float64
	// Disparity is how far apart the two eyes put the NEAREST thing, in pixels
	// of the source. Zero uses 24.
	//
	// Small on purpose: a large disparity makes an impressive still and an
	// unwatchable film, because the eyes must converge differently on every
	// cut.
	Disparity int
}

// softenRadius is how much the depth map is blurred before it moves any
// pixels. Two, measured: it takes the worst movement of an edge between one
// frame and the next from 4.94 pixels to 1.10, for 4.8% less relief.
const softenRadius = 2

// disparityOrDefault resolves the requested eye separation.
func (c Config) disparityOrDefault() int {
	if c.Disparity <= 0 {
		return 24
	}
	return c.Disparity
}

func (c Config) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(fmt.Sprintf(format, args...))
	}
}
