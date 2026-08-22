// Command xrplay plays immersive video on XR glasses.
//
//	xrplay film.mp4                      # detect everything, pick the glasses
//	xrplay -screen "Built-in" film.mp4   # play on the laptop panel instead
//	xrplay -proj 360 -layout sbs f.mp4   # override the detection
//	xrplay -mono -screen Built-in f.mp4  # one eye, for looking at on a monitor
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/go-xrkit/player"
	"github.com/go-xrkit/xrkit/projection"
	"github.com/go-xrkit/xrkit/stereo"
)

// init pins the main goroutine to the process main OS thread, before anything
// else in this program runs.
//
// AppKit refuses to create an NSWindow anywhere else, and window.Open's own
// LockOSThread is TOO LATE: it locks the goroutine to whatever thread it is on
// by then, and a foreign call made earlier -- opening the decoder, in this
// program -- can leave the main goroutine resumed on a different thread. The
// failure is intermittent, which is worse than reliable: it depends on
// scheduling, so a build can work repeatedly and then abort with
// "NSWindow should only be instantiated on the main thread!".
//
// init runs on the main goroutine before main, which is the earliest this can
// be said.
func init() {
	runtime.LockOSThread()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "xrplay:", err)
		os.Exit(1)
	}
}

// projections and layouts are the override vocabularies, kept here so the flag
// help and the parsing cannot drift apart.
var projections = map[string]projection.Projection{
	"flat":    projection.Screen,
	"360":     projection.Sphere360,
	"180":     projection.Hemisphere180,
	"fisheye": projection.Fisheye180,
}

var layouts = map[string]stereo.Layout{
	"mono": stereo.Mono,
	"sbs":  stereo.SideBySide,
	"ou":   stereo.OverUnder,
}

func run() error {
	var (
		screen = flag.String("screen", "", "display to play on, matched by name (default: the glasses, else the widest external)")
		proj   = flag.String("proj", "", "override the source geometry: flat, 360, 180, fisheye")
		layout = flag.String("layout", "", "override the eye packing: mono, sbs, ou")
		swap   = flag.Bool("swap", false, "the eye images are the other way round")
		fov    = flag.Float64("fov", 0, "vertical field of view in degrees (default 90)")
		mono   = flag.Bool("mono", false, "render one eye even on a side-by-side display")
		loop   = flag.Bool("loop", false, "restart at the end")
		quiet  = flag.Bool("quiet", false, "print nothing but errors")
		for_   = flag.Duration("for", 0, "stop after this long (e.g. 10s); 0 plays to the end")
		snap   = flag.String("snapshot", "", "write the composed framebuffer to this PNG")
		snapAt = flag.Int("snapshot-after", 0, "how many frames to show before the snapshot")
	)
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		return fmt.Errorf("expected exactly one file, got %d", flag.NArg())
	}

	cfg := player.Config{
		Path:          flag.Arg(0),
		Screen:        *screen,
		FOVyDeg:       *fov,
		Mono:          *mono,
		Loop:          *loop,
		For:           *for_,
		Snapshot:      *snap,
		SnapshotAfter: *snapAt,
	}
	if !*quiet {
		cfg.Log = func(s string) { fmt.Println(s) }
	}

	// An override is all-or-nothing on each axis, but the two axes are
	// independent, so start from the detection and replace only what was asked
	// for. Detecting needs the frame size, which Play has and this does not, so
	// the override is expressed as a patch Play applies.
	if *proj != "" || *layout != "" || *swap {
		g := &player.Geometry{}
		if *proj != "" {
			p, ok := projections[*proj]
			if !ok {
				return fmt.Errorf("unknown -proj %q; want one of flat, 360, 180, fisheye", *proj)
			}
			g.Projection = p
		} else {
			g.Projection = projection.Screen
		}
		if *layout != "" {
			l, ok := layouts[*layout]
			if !ok {
				return fmt.Errorf("unknown -layout %q; want one of mono, sbs, ou", *layout)
			}
			g.Format.Layout = l
		}
		g.Format.Swapped = *swap
		cfg.Geometry = g
	}

	// Play must run on the main goroutine: the window back-end pins that thread
	// for AppKit, which requires it.
	return player.Play(cfg)
}
