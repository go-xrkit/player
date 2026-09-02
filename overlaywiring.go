package player

import "github.com/go-widgets/toolkit"

// activityWidget notices that the viewer is doing something and passes the event
// on unchanged.
//
// It draws nothing and decides nothing: it exists because the controls hide
// themselves after a few idle seconds, and while the pointer is OVER the bar the
// events go to the bar rather than to the video underneath — so without this the
// overlay would vanish from under the hand using it.
type activityWidget struct {
	toolkit.Widget
	seen func()
}

func (a *activityWidget) OnEvent(ev toolkit.Event) {
	a.seen()
	a.Widget.OnEvent(ev)
}

// controlsOverlay is the composition a viewer actually looks at: the video, with
// the transport controls layered above it, appearing when something happens and
// hiding again on their own.
//
// It is a type rather than wiring inlined in Play because when it WAS inlined
// there was no way to check whether the bar appeared except to look at a screen,
// and the wiring was wrong in a way looking at a screen only revealed after
// three idle seconds. Everything a test needs to drive is here, so the test and
// the player share one composition instead of two that resemble each other.
type controlsOverlay struct {
	// Overlay is the widget tree to hand to the window.
	Overlay *toolkit.Overlay

	vis   *barVisibility
	layer toolkit.Widget
	// pointer is drawn above the bar, because a pointer BEHIND the control it
	// is over is worse than no pointer at all.
	pointer toolkit.Widget
}

// newControlsOverlay layers bar above content. The bar is not shown yet; the
// first Tick decides that.
func newControlsOverlay(content, bar, pointer toolkit.Widget) *controlsOverlay {
	c := &controlsOverlay{vis: newBarVisibility(), pointer: pointer}
	c.Overlay = toolkit.NewOverlay(content)
	c.layer = &activityWidget{Widget: bar, seen: c.vis.Note}
	return c
}

// Note records viewer activity that did not arrive through the bar itself — a
// key press, or the pointer moving over the video.
func (c *controlsOverlay) Note() { c.vis.Note() }

// Tick applies the visibility rule and reports whether the layers changed, which
// is the only case worth repainting for.
//
// The bar is pushed and cleared rather than hidden, because a layer that is not
// there cannot swallow a click meant for the video.
func (c *controlsOverlay) Tick() bool {
	show, changed := c.vis.Update()
	if !changed {
		return false
	}
	if show {
		if len(c.Overlay.Layers) == 0 {
			c.Overlay.Push(c.layer)
			// The pointer comes and goes with the controls, which is the
			// right rule for both: it appears the moment the viewer moves
			// the mouse, and it stops sitting on top of the film once they
			// have left it alone.
			if c.pointer != nil {
				c.Overlay.Push(c.pointer)
			}
		}
	} else {
		c.Overlay.Clear()
	}
	return true
}

// Shown reports whether the controls are currently up.
func (c *controlsOverlay) Shown() bool { return c.vis.Shown() }
