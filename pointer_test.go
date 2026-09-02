package player

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

func TestNoPointerUntilItMoves(t *testing.T) {
	l := newPointerLayer()
	l.Layout(1920, 1200, 1)
	if got := l.marks(); got != nil {
		t.Errorf("a pointer was drawn at %v before the mouse moved", got)
	}
	l.Moved(100, 200)
	if got := l.marks(); len(got) != 1 {
		t.Fatalf("%d marks after the mouse moved, want 1", len(got))
	}
}

func TestThePointerIsWhereTheMouseIs(t *testing.T) {
	l := newPointerLayer()
	l.Layout(1920, 1200, 1)
	l.Moved(640, 480)
	m := l.marks()
	if len(m) != 1 {
		t.Fatalf("%d marks on a single view, want 1", len(m))
	}
	// The tip of the glyph is its top-left corner, so that is where the
	// position goes: a mark centred on it would report a click half a mark from
	// where it lands.
	if m[0].X != 640 || m[0].Y != 480 {
		t.Errorf("the mark is at %d,%d for a pointer at 640,480", m[0].X, m[0].Y)
	}
	if m[0].W != pointerSize || m[0].H != pointerSize {
		t.Errorf("the mark is %dx%d, want %d square", m[0].W, m[0].H, pointerSize)
	}
}

// TestBothEyesShowThePointer is the case that matters in the glasses. The panel
// reports ONE position; in a side-by-side mode a single mark lands in one eye
// only, which the viewer sees as a smear at infinity rather than as a pointer.
func TestBothEyesShowThePointer(t *testing.T) {
	l := newPointerLayer()
	l.Layout(3840, 1200, 2)
	for _, at := range []struct{ x, wantWithin int }{
		{100, 100},  // in the left eye
		{2020, 100}, // the same place in the right eye
	} {
		l.Moved(at.x, 300)
		m := l.marks()
		if len(m) != 2 {
			t.Fatalf("pointer at %d: %d marks, want one per eye", at.x, len(m))
		}
		if m[0].X != at.wantWithin {
			t.Errorf("pointer at %d: the left mark is at %d, want %d", at.x, m[0].X, at.wantWithin)
		}
		if m[1].X != 1920+at.wantWithin {
			t.Errorf("pointer at %d: the right mark is at %d, want %d", at.x, m[1].X, 1920+at.wantWithin)
		}
		if m[0].Y != m[1].Y {
			t.Errorf("the two marks are at different heights (%d and %d), so they have no depth", m[0].Y, m[1].Y)
		}
	}
}

func TestAPointerWithNowhereToGo(t *testing.T) {
	// A layer never laid out has no eye to put a mark in, and must not divide
	// by the width it does not have.
	l := newPointerLayer()
	l.Moved(10, 10)
	if got := l.marks(); got != nil {
		t.Errorf("marks %v from a layer that was never laid out", got)
	}
	// Nor may a nonsense eye count leave it dividing by zero.
	l.Layout(1920, 1200, 0)
	l.Moved(10, 10)
	if got := l.marks(); len(got) != 1 {
		t.Errorf("%d marks for a view with no stated eye count, want 1", len(got))
	}
	// A position to the left of the panel is clamped rather than wrapped: Go's
	// remainder keeps the sign, so a negative x would put the mark at the far
	// RIGHT of the eye, which is the opposite of where the mouse is.
	l.Layout(1920, 1200, 1)
	l.Moved(-5, 20)
	if m := l.marks(); len(m) != 1 || m[0].X != 0 {
		t.Errorf("a pointer left of the panel gave %v, want a mark at x=0", m)
	}
}

// TestThePointerActuallyDraws goes past marks(): it renders and counts ink.
//
// The placement can be right while nothing appears -- an icon name that is not
// in the pack returns an empty document, and toolkit.SVGIcon then draws
// nothing at all, silently. That is a plausible failure and it would look
// exactly like the bug this whole file exists to fix.
func TestThePointerActuallyDraws(t *testing.T) {
	const w, h = 640, 400
	const gr, gg, gb = 40, 44, 52
	buf := make([]byte, w*h*4)
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = gr, gg, gb, 255
	}
	p := painter.NewPixelPainter(buf, w, h)

	l := newPointerLayer()
	l.Layout(w, h, 1)
	l.Moved(100, 120)
	l.Draw(p, toolkit.DefaultDark())

	inside, outside := 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			if buf[i] == gr && buf[i+1] == gg && buf[i+2] == gb {
				continue
			}
			if x >= 100 && x < 100+pointerSize && y >= 120 && y < 120+pointerSize {
				inside++
			} else {
				outside++
			}
		}
	}
	if inside == 0 {
		t.Error("nothing was drawn where the pointer is; the icon name is probably not in the pack")
	}
	if outside != 0 {
		t.Errorf("%d pixels were painted outside the mark, which would smear over the film", outside)
	}
	// A mark is a mark, not a filled square: a glyph that covered its whole box
	// would be a blob rather than a pointer.
	if area := pointerSize * pointerSize; inside > area/2 {
		t.Errorf("%d of %d pixels in the box are ink; that is a blob, not a pointer", inside, area)
	}

	// The negative control: an untouched layer paints nothing at all, so the
	// count above is about the pointer and not about the painter.
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2] = gr, gg, gb
	}
	quiet := newPointerLayer()
	quiet.Layout(w, h, 1)
	quiet.Draw(p, toolkit.DefaultDark())
	for i := 0; i < len(buf); i += 4 {
		if buf[i] != gr || buf[i+1] != gg || buf[i+2] != gb {
			t.Fatal("a pointer that never moved painted something anyway")
		}
	}
}
