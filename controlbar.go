package player

import (
	"fmt"
	"time"
)

// barHeight is the strip the controls occupy at the bottom of the view, in
// framebuffer pixels at 1x. It is generous because this is read through a
// headset at arm's length, not on a desk monitor.
const barHeight = 120

// barMargin keeps the controls off the very edge of the panel. XR glasses lose
// the outermost pixels to the optics, so a bar flush with the bottom is a bar
// partly outside what the wearer can see.
const barMargin = 24

// Rect is a rectangle in framebuffer pixels. It mirrors the toolkit's own shape
// so the layout can be computed and tested without a widget tree.
type Rect struct{ X, Y, W, H int }

// layoutBar places the control bar across the bottom of a fbW x fbH view.
//
// It computes only WHERE THE BAR SITS. What goes inside it is laid out by the
// toolkit boxes, which is their job; this is the part with a real failure mode,
// so it is a plain function over three integers and is checked without a window.
// A bar that lands off-screen, or under the wearer's blind edge, is not
// something to discover while wearing the headset.
//
// eyes is how many views the panel shows. In a side-by-side 3D mode the controls
// belong in the LEFT eye's half only: drawn across the whole panel they would
// appear twice, at different depths, and read as a double image.
func layoutBar(fbW, fbH, eyes int) Rect {
	if fbW <= 0 || fbH <= 0 {
		return Rect{}
	}
	if eyes < 1 {
		eyes = 1
	}
	w := fbW/eyes - 2*barMargin
	if w < 0 {
		w = 0
	}
	h := barHeight
	if h > fbH {
		h = fbH
	}
	y := fbH - h - barMargin
	if y < 0 {
		y = 0
	}
	return Rect{X: barMargin, Y: y, W: w, H: h}
}

// formatTime renders a position the way a player does: minutes and seconds, with
// hours only when there are any.
func formatTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// seekTarget turns a slider position in [0,1] into a place in the file, clamped
// to the file.
func seekTarget(fraction float64, total time.Duration) time.Duration {
	if total <= 0 {
		return 0
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	return time.Duration(fraction * float64(total))
}

// progressFraction is the reverse: where the slider sits for a position.
func progressFraction(at, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	f := float64(at) / float64(total)
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// hideAfter is how long the controls stay up once the pointer stops moving. It
// is the convention every video player shares, and the reason is the same here:
// an overlay that never leaves is furniture in the middle of the film.
const hideAfter = 3 * time.Second
