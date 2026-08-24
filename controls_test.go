package player

import (
	"testing"
	"time"
)

func TestKeyAction(t *testing.T) {
	for _, tc := range []struct {
		code string
		want Action
	}{
		{"Escape", ActionQuit}, {"q", ActionQuit}, {"Q", ActionQuit},
		{" ", ActionTogglePause}, {"k", ActionTogglePause}, {"K", ActionTogglePause},
		{"ArrowLeft", ActionSeekBack}, {"j", ActionSeekBack}, {"J", ActionSeekBack},
		{"ArrowRight", ActionSeekForward}, {"l", ActionSeekForward}, {"L", ActionSeekForward},
		{"ArrowDown", ActionVolumeDown},
		{"ArrowUp", ActionVolumeUp},
	} {
		if got := KeyAction(tc.code); got != tc.want {
			t.Errorf("KeyAction(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// TestUnknownKeysDoNothing is the regression test for a real annoyance: any key
// used to END playback, so brushing the keyboard closed the film. A viewer in a
// headset cannot see what they pressed.
func TestUnknownKeysDoNothing(t *testing.T) {
	for _, code := range []string{"a", "Enter", "Tab", "F1", "", "é", "Shift", "1"} {
		if got := KeyAction(code); got != ActionNone {
			t.Errorf("KeyAction(%q) = %v, want ActionNone", code, got)
		}
	}
}

func TestActionString(t *testing.T) {
	for _, tc := range []struct {
		a    Action
		want string
	}{
		{ActionNone, "none"}, {ActionQuit, "quit"}, {ActionTogglePause, "pause/resume"},
		{ActionSeekBack, "seek back"}, {ActionSeekForward, "seek forward"},
		{ActionVolumeDown, "volume down"}, {ActionVolumeUp, "volume up"},
		{Action(99), "none"},
	} {
		if got := tc.a.String(); got != tc.want {
			t.Errorf("Action(%d).String() = %q, want %q", int(tc.a), got, tc.want)
		}
	}
}

func TestClampVolume(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {1.5, 1},
	} {
		if got := clampVolume(tc.in); got != tc.want {
			t.Errorf("clampVolume(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestPauserAccountsForTheTimeItStopped is the property that matters: a pull
// decoder times each frame against the wall clock, so if the paused time were
// not given back, every frame would be late by the length of the pause and the
// video would race to catch up.
func TestPauserAccountsForTheTimeItStopped(t *testing.T) {
	now := time.Unix(0, 0)
	p := newPauser()
	p.now = func() time.Time { return now }

	if p.Paused() {
		t.Fatal("a new pauser is paused")
	}
	if p.Offset() != 0 {
		t.Fatalf("a new pauser reports %v of pause", p.Offset())
	}

	if !p.Toggle() {
		t.Fatal("Toggle did not report paused")
	}
	if !p.Paused() {
		t.Fatal("Paused() = false after Toggle")
	}
	// While paused, the offset grows with the clock.
	now = now.Add(3 * time.Second)
	if got := p.Offset(); got != 3*time.Second {
		t.Errorf("Offset() while paused = %v, want 3s", got)
	}

	if p.Toggle() {
		t.Fatal("second Toggle reported still paused")
	}
	if got := p.Offset(); got != 3*time.Second {
		t.Errorf("Offset() after resuming = %v, want the 3s it was stopped", got)
	}
	// Running time does not add to the offset.
	now = now.Add(10 * time.Second)
	if got := p.Offset(); got != 3*time.Second {
		t.Errorf("Offset() after running = %v, want still 3s", got)
	}
	// A second pause accumulates.
	p.Toggle()
	now = now.Add(2 * time.Second)
	p.Toggle()
	if got := p.Offset(); got != 5*time.Second {
		t.Errorf("Offset() after two pauses = %v, want 5s", got)
	}
}

func TestPauserWaitReturnsAtOnceWhenRunning(t *testing.T) {
	p := newPauser()
	done := make(chan struct{})
	go func() { p.Wait(make(chan struct{})); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait blocked while not paused")
	}
}

func TestPauserWaitWakesOnResume(t *testing.T) {
	p := newPauser()
	p.Toggle()
	done := make(chan struct{})
	go func() { p.Wait(make(chan struct{})); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned while paused")
	case <-time.After(50 * time.Millisecond):
	}
	p.Toggle()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not wake on resume")
	}
}

// TestPauserWaitWakesOnStop matters because a paused player must still be
// closable: without this, quitting while paused would hang.
func TestPauserWaitWakesOnStop(t *testing.T) {
	p := newPauser()
	p.Toggle()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { p.Wait(stop); close(done) }()
	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not wake on stop; quitting while paused would hang")
	}
}

// TestBarVisibilityComesBackAfterHiding is the regression test for the bug that
// made the controls appear exactly once.
//
// The first version noted activity by setting the "shown" flag directly, so the
// ticker compared the flag to itself, found no change, and never pushed the
// overlay again. Every part looked right on its own; only the sequence was
// wrong, which is why the sequence is what this walks.
func TestBarVisibilityComesBackAfterHiding(t *testing.T) {
	now := time.Unix(1000, 0)
	v := newBarVisibility()
	v.now = func() time.Time { return now }
	v.last = now

	// It starts up, so the first Update reports the change from nothing.
	if show, changed := v.Update(); !show || !changed {
		t.Fatalf("first Update = (%v,%v), want shown and changed", show, changed)
	}
	// Asking again while nothing happens is not a change.
	if show, changed := v.Update(); !show || changed {
		t.Fatalf("second Update = (%v,%v), want shown and unchanged", show, changed)
	}

	// Idle past the timeout: it hides, once.
	now = now.Add(hideAfter + time.Second)
	if show, changed := v.Update(); show || !changed {
		t.Fatalf("after idling = (%v,%v), want hidden and changed", show, changed)
	}
	if show, changed := v.Update(); show || changed {
		t.Fatalf("still idle = (%v,%v), want hidden and unchanged", show, changed)
	}

	// THE BUG: moving the pointer again must bring it back, and must report the
	// change so the caller actually pushes the layer.
	v.Note()
	show, changed := v.Update()
	if !show {
		t.Fatal("after activity the bar is not shown; it appeared once and never again")
	}
	if !changed {
		t.Fatal("after activity the bar is shown but the change was not REPORTED; " +
			"the caller never pushes the overlay, which is exactly the bug")
	}

	// And it hides again after the next idle stretch, so this is a cycle and not
	// a one-shot.
	now = now.Add(hideAfter + time.Second)
	if show, changed := v.Update(); show || !changed {
		t.Fatalf("second hide = (%v,%v), want hidden and changed", show, changed)
	}
	v.Note()
	if show, changed := v.Update(); !show || !changed {
		t.Fatalf("second show = (%v,%v), want shown and changed", show, changed)
	}
}

func TestBarVisibilityShown(t *testing.T) {
	now := time.Unix(0, 0)
	v := newBarVisibility()
	v.now = func() time.Time { return now }
	v.last = now
	if v.Shown() {
		t.Error("Shown() is true before any Update decided anything")
	}
	v.Update()
	if !v.Shown() {
		t.Error("Shown() is false after an Update that showed it")
	}
	now = now.Add(hideAfter * 2)
	v.Update()
	if v.Shown() {
		t.Error("Shown() is true after an Update that hid it")
	}
}

// TestBarVisibilityKeepsUpUnderRepeatedActivity is the case of a viewer moving
// the pointer continuously: the bar must stay up, and must not be pushed again
// on every move.
func TestBarVisibilityKeepsUpUnderRepeatedActivity(t *testing.T) {
	now := time.Unix(0, 0)
	v := newBarVisibility()
	v.now = func() time.Time { return now }
	v.last = now
	v.Update() // up, changed

	pushes := 0
	for i := 0; i < 20; i++ {
		now = now.Add(hideAfter / 4)
		v.Note()
		if show, changed := v.Update(); !show {
			t.Fatalf("iteration %d: the bar hid while the pointer was moving", i)
		} else if changed {
			pushes++
		}
	}
	if pushes != 0 {
		t.Errorf("continuous movement reported %d changes; the layer would be pushed that many times", pushes)
	}
}
