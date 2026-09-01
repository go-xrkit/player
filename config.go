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
	// FOVyDeg is the vertical field of view of each eye's view, in degrees.
	// Zero uses 90, which is about what these glasses present.
	FOVyDeg float64
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

// fovOrDefault resolves the requested field of view.
func (c Config) fovOrDefault() float64 {
	if c.FOVyDeg <= 0 {
		return 90
	}
	return c.FOVyDeg
}

func (c Config) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(fmt.Sprintf(format, args...))
	}
}
