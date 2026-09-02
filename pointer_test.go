package player

import "testing"

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
