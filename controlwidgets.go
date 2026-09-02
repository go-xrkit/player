package player

import (
	"time"

	"github.com/go-icons/iconoir"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// barActions is what the control bar asks the player to do. Every field may be
// nil, in which case that button is built but inert — a bar over a source with
// no clock still shows what it cannot do rather than silently omitting it,
// because a missing button reads as a broken build.
type barActions struct {
	Restart     func()
	Back        func()
	TogglePause func()
	Forward     func()
	ToggleMute  func()
	// SeekTo is given a position in [0,1] when the slider is moved.
	SeekTo func(fraction float64)
}

// controlBar is the transport overlay: the widgets, and the state they show.
//
// Every visible element is a toolkit widget and every glyph is an Iconoir
// drawing through Button's icon seam. Nothing here paints a pixel of its own —
// a control bar drawn by hand would be a private set of shapes that no theme,
// no HiDPI scale and no accessibility walk knows anything about.
type controlBar struct {
	// frame is the scrim with the controls stacked on it. A control bar with no
	// ground is unreadable over a bright frame -- white text on a white shot --
	// which is why every player puts a dark veil behind its controls.
	frame   *toolkit.Overlay
	ground  *toolkit.Backdrop
	root    *toolkit.VBox
	times   *toolkit.HBox
	rows    *toolkit.HBox
	buttons []*toolkit.IconButton
	scrub   *toolkit.Scale
	elapsed *toolkit.Label
	total   *toolkit.Label

	playing bool
	muted   bool
	// seeking suppresses the position updates that would fight the user's drag.
	seeking  bool
	duration time.Duration
}

// icon wires an Iconoir glyph into Button's icon seam. The seam hands the
// button's current ink, so the glyph follows every hover, press and disabled
// state without this knowing what those look like.
func icon(name string) func(painter.Painter, toolkit.Rect, toolkit.RGBA) {
	// go-icons ships the pack as SVG source; toolkit.SVGIcon rasterises and
	// caches it per (document, ink). Resolved once here rather than per draw.
	return toolkit.SVGIcon(iconoir.Icon(name))
}

// The Iconoir names used, in the order the buttons appear. They are the standard
// transport set every player shows, which is the point: a viewer should not have
// to learn this one.
const (
	iconRestart = "skip-prev"
	iconBack    = "backward-15-seconds"
	iconPlay    = "play"
	iconPause   = "pause"
	iconForward = "forward-15-seconds"
	iconSound   = "sound-high"
	iconMuted   = "sound-off"
)

// The fixed sizes the boxes are told. They are generous because this is read
// through a headset at arm's length, not on a desk monitor.
// scrimFill is the veil behind the controls. It is dark and nearly opaque
// rather than a subtle tint: this is read through XR optics that wash blacks
// out, so a scrim tuned for a desk monitor disappears in the headset.
var scrimFill = painter.RGBA{R: 12, G: 14, B: 18, A: 235}

const (
	buttonSize = 56
	// buttonGap keeps the squares apart. It is wide because these are pressed
	// through a headset, where the pointer is a guess rather than a placement.
	buttonGap = 16
	// barPadding keeps the contents off the scrim's rounded edge. Without it
	// the elapsed time is drawn touching the corner, which reads as clipped
	// text rather than as a margin nobody left.
	barPadding     = 24
	timeLabelWidth = 90
	scrubRowHeight = 36
)

// newControlBar builds the bar. It is not laid out yet; call [controlBar.Layout]
// once the framebuffer size is known.
func newControlBar(a barActions) *controlBar {
	b := &controlBar{
		ground:  &toolkit.Backdrop{Fill: scrimFill, Radius: 14},
		root:    toolkit.NewVBox(),
		times:   toolkit.NewHBox(),
		rows:    toolkit.NewHBox(),
		scrub:   toolkit.NewScale(0, 1, 0),
		elapsed: toolkit.NewLabel("0:00"),
		total:   toolkit.NewLabel("0:00"),
	}

	mk := func(name string, on func()) *toolkit.IconButton {
		btn := toolkit.NewIconButton("", on)
		btn.Glyph = icon(name)
		btn.Flat = true
		btn.Disabled().Set(on == nil)
		return btn
	}
	play := toolkit.NewIconButton("", a.TogglePause)
	play.Flat = true
	play.Disabled().Set(a.TogglePause == nil)
	// One button, two glyphs: it shows what pressing it will DO, which is the
	// convention, rather than what the player is currently doing.
	play.Glyph = func(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) {
		name := iconPlay
		if b.playing {
			name = iconPause
		}
		icon(name)(p, r, ink)
	}
	mute := toolkit.NewIconButton("", a.ToggleMute)
	mute.Flat = true
	mute.Disabled().Set(a.ToggleMute == nil)
	mute.Glyph = func(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) {
		name := iconSound
		if b.muted {
			name = iconMuted
		}
		icon(name)(p, r, ink)
	}

	b.buttons = []*toolkit.IconButton{
		mk(iconRestart, a.Restart),
		mk(iconBack, a.Back),
		play,
		mk(iconForward, a.Forward),
		mute,
	}
	// The buttons are a centred group of fixed squares: PackCenter splits the
	// slack either side, which is what keeps the transport controls in the middle
	// of the bar whatever the panel is wide.
	b.rows.Pack = toolkit.PackCenter
	b.rows.Align = toolkit.BoxAlignCenter
	// The transport buttons are separate targets, not a segmented control, so
	// they are spaced apart. At the default 4 pixels five 56-pixel squares read
	// as one striped slab -- which is what this looked like.
	b.rows.Spacing = buttonGap
	for _, btn := range b.buttons {
		b.rows.AddFixed(btn, buttonSize)
	}

	if a.SeekTo != nil {
		b.scrub.Value().Subscribe(func(f float64) {
			if b.seeking {
				return
			}
			a.SeekTo(f)
		})
	} else {
		b.scrub.Disabled().Set(true)
	}

	// Top row: the two times flank the slider, which takes what is left.
	b.times.AddFixed(b.elapsed, timeLabelWidth)
	b.times.AddFlex(b.scrub, 1)
	b.times.AddFixed(b.total, timeLabelWidth)

	b.root.AddFixed(b.times, scrubRowHeight)
	b.root.AddFlex(b.rows, 1)

	b.frame = toolkit.NewOverlay(b.ground)
	b.frame.Push(b.root)
	return b
}

// Root is the widget to hand to an Overlay layer.
func (b *controlBar) Root() toolkit.Widget { return b.frame }

// Layout places the bar for a framebuffer of fbW x fbH showing eyes views. The
// boxes inside it lay their own children out.
func (b *controlBar) Layout(fbW, fbH, eyes int) {
	r := layoutBar(fbW, fbH, eyes)
	rect := toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
	// The Overlay resizes its Content, so the scrim follows on its own; the
	// controls are a LAYER and position themselves.
	b.frame.SetBounds(rect)
	b.root.SetBounds(toolkit.Rect{
		X: rect.X + barPadding,
		Y: rect.Y + barPadding/2,
		W: rect.W - 2*barPadding,
		H: rect.H - barPadding,
	})
}

// SetPlaying updates the play/pause glyph.
func (b *controlBar) SetPlaying(playing bool) { b.playing = playing }

// SetMuted updates the sound glyph.
func (b *controlBar) SetMuted(muted bool) { b.muted = muted }

// SetProgress moves the slider and the labels.
//
// It does nothing while the viewer is dragging: a position arriving from the
// clock mid-drag would fight the thumb under their finger and make the control
// feel broken.
func (b *controlBar) SetProgress(at, total time.Duration) {
	b.duration = total
	b.elapsed.Text().Set(formatTime(at))
	b.total.Text().Set(formatTime(total))
	if b.seeking {
		return
	}
	b.seeking = true
	b.scrub.SetValue(progressFraction(at, total))
	b.seeking = false
}
