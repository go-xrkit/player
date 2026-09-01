//go:build !darwin

package player

import (
	"errors"
)

// ErrUnsupported is returned by [Play] on every platform without a decoder
// back-end. The geometry and display-choosing logic in this package is portable
// and tested everywhere; only playback is not.
var ErrUnsupported = errors.New("player: playback is implemented on macOS only")

// Play reports [ErrUnsupported]. The Linux and Windows routes exist in
// principle -- VA-API or Media Foundation for decode, the X11/Wayland/Win32
// back-ends of go-widgets/window for the display -- and are simply not written.
func Play(Config) error { return ErrUnsupported }
