package player

import (
	"sync"
	"time"
)

// Action is what a key press does during playback.
type Action int

// The actions a viewer can take. There is no on-screen control: the window is
// full screen with no chrome, so the keyboard is the whole interface and every
// binding has to be one someone would guess.
const (
	// ActionNone is a key with no meaning here, which is most of them.
	ActionNone Action = iota
	// ActionQuit ends playback and closes the window.
	ActionQuit
	// ActionTogglePause stops and restarts the clock.
	ActionTogglePause
	// ActionSeekBack and ActionSeekForward move by [SeekStep].
	ActionSeekBack
	ActionSeekForward
	// ActionVolumeDown and ActionVolumeUp move by [VolumeStep].
	ActionVolumeDown
	ActionVolumeUp
)

// String names the action.
func (a Action) String() string {
	switch a {
	case ActionQuit:
		return "quit"
	case ActionTogglePause:
		return "pause/resume"
	case ActionSeekBack:
		return "seek back"
	case ActionSeekForward:
		return "seek forward"
	case ActionVolumeDown:
		return "volume down"
	case ActionVolumeUp:
		return "volume up"
	}
	return "none"
}

// SeekStep and VolumeStep are how far one key press moves.
const (
	SeekStep   = 10 * time.Second
	VolumeStep = 0.1
)

// KeyAction maps a key name to what it does.
//
// The names are the DOM-style ones the toolkit reports ("ArrowLeft", "Escape"),
// with printable keys arriving as themselves. Both the arrows and j/k/l are
// bound, because j/k/l is what every video player has taught people and the
// arrows are what everyone tries first — binding only one of them would be a
// preference imposed on a viewer wearing a headset who cannot see a help screen.
//
// An unknown key does nothing. It deliberately does NOT quit: this used to end
// playback on ANY key, which meant brushing the keyboard closed the film.
func KeyAction(code string) Action {
	switch code {
	case "Escape", "q", "Q":
		return ActionQuit
	case " ", "k", "K":
		return ActionTogglePause
	case "ArrowLeft", "j", "J":
		return ActionSeekBack
	case "ArrowRight", "l", "L":
		return ActionSeekForward
	case "ArrowDown":
		return ActionVolumeDown
	case "ArrowUp":
		return ActionVolumeUp
	}
	return ActionNone
}

// clampVolume holds a volume inside the range the system accepts.
func clampVolume(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// pauser stops a source that has no clock of its own, without losing its place.
//
// A pull decoder times each frame against the wall clock, so simply not decoding
// would make every frame late by however long the pause lasted and the video
// would then race to catch up. So the paused time is accumulated and handed back
// through [pauser.Offset], which the timing subtracts.
type pauser struct {
	mu     sync.Mutex
	paused bool
	since  time.Time
	total  time.Duration
	wake   chan struct{}
	// now is the clock, swappable so the accounting can be tested without
	// sleeping.
	now func() time.Time
}

// newPauser returns a running pauser.
func newPauser() *pauser {
	return &pauser{wake: make(chan struct{}), now: time.Now}
}

// Toggle flips between paused and running, and reports the new paused state.
func (p *pauser) Toggle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused {
		p.total += p.now().Sub(p.since)
		p.paused = false
		close(p.wake)
		p.wake = make(chan struct{})
		return false
	}
	p.paused = true
	p.since = p.now()
	return true
}

// Paused reports whether playback is stopped.
func (p *pauser) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// Offset is how long playback has been stopped in total, which the timing must
// subtract from the elapsed wall time.
func (p *pauser) Offset() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused {
		return p.total + p.now().Sub(p.since)
	}
	return p.total
}

// Wait blocks while paused, returning early if stop closes. It is safe to call
// when not paused, where it returns at once.
func (p *pauser) Wait(stop <-chan struct{}) {
	for {
		p.mu.Lock()
		if !p.paused {
			p.mu.Unlock()
			return
		}
		wake := p.wake
		p.mu.Unlock()
		select {
		case <-wake:
		case <-stop:
			return
		}
	}
}
