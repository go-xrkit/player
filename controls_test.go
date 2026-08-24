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
