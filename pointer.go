package player

import (
	"github.com/go-icons/iconoir"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// iconPointer is the mark drawn where the mouse is.
const iconPointer = "cursor-pointer"

// pointerSize is how big that mark is, in pixels. It is larger than a desktop
// cursor because it is read through XR optics at a fraction of the panel's
// nominal resolution.
const pointerSize = 40

// pointerLayer draws the mouse pointer into the picture.
//
// It exists because a full-screen window on a pair of glasses SWALLOWS the
// pointer. The system cursor is drawn by the compositor onto the desktop; the
// glasses are showing this window's pixels, so a pointer that wanders onto that
// display is simply not visible anywhere, and the only way to find it again is
// to unplug the glasses. That was measured, and it is written down in
// go-widgets' window.Config.Passive, whose answer is to take no input at all --
// which would cost the keyboard, and this player has a full set of keys.
//
// Drawing it instead costs nothing and keeps every control usable: the picture
// belongs to this program, so it can put the pointer back into it.
//
// In a side-by-side mode the mark is drawn ONCE PER EYE, at the same position
// within each. A single mark would land in one eye only, which the viewer sees
// as a smear at infinity rather than as a pointer.
type pointerLayer struct {
	toolkit.Base
	glyph func(painter.Painter, toolkit.Rect, toolkit.RGBA)
	// eyes is how many views the framebuffer is split into, and eyeW how wide
	// one of them is.
	eyes, eyeW int
	x, y       int
	// seen stays false until the pointer has actually moved. Drawing a mark at
	// the origin before then would put a cursor in the corner of a film nobody
	// has touched.
	seen bool
}

// newPointerLayer builds the layer. Layout must be called before it draws.
func newPointerLayer() *pointerLayer {
	return &pointerLayer{glyph: toolkit.SVGIcon(iconoir.Icon(iconPointer)), eyes: 1}
}

// Layout tells the layer the shape of the framebuffer it is drawing into.
func (l *pointerLayer) Layout(fbW, fbH, eyes int) {
	if eyes < 1 {
		eyes = 1
	}
	l.eyes, l.eyeW = eyes, fbW/eyes
	l.SetBounds(toolkit.Rect{W: fbW, H: fbH})
}

// Moved records where the pointer is, in framebuffer coordinates.
func (l *pointerLayer) Moved(x, y int) {
	l.x, l.y, l.seen = x, y, true
}

// marks are the rectangles the mark is drawn in: one per eye, all at the same
// place within their own eye.
//
// It is separate from Draw so the placement can be checked without a painter,
// which is the part that can be wrong.
func (l *pointerLayer) marks() []toolkit.Rect {
	if !l.seen || l.eyeW <= 0 {
		return nil
	}
	// Which eye the pointer is actually in, and where inside it. The panel
	// reports one position; both eyes must show it in the same place, or the
	// two images disagree and the mark has no depth.
	within := l.x % l.eyeW
	if within < 0 {
		within = 0
	}
	out := make([]toolkit.Rect, 0, l.eyes)
	for eye := 0; eye < l.eyes; eye++ {
		// The tip of a pointer glyph is its top-left corner, which is where the
		// pointer IS -- a mark centred on the position would report a click
		// half a mark away from where it lands.
		out = append(out, toolkit.Rect{
			X: eye*l.eyeW + within,
			Y: l.y,
			W: pointerSize,
			H: pointerSize,
		})
	}
	return out
}

// Draw paints the mark. Every pixel of it comes from the toolkit's SVG icon
// path, so it follows the theme's ink and the icon pack -- nothing here is a
// shape this package invented.
func (l *pointerLayer) Draw(p painter.Painter, theme *toolkit.Theme) {
	for _, r := range l.marks() {
		l.glyph(p, r, theme.OnSurface)
	}
}
