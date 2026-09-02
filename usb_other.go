//go:build !darwin

package player

import "github.com/go-xrkit/xrkit/glasses"

// usbDevices lists nothing off macOS: the player only plays there, and this
// exists so the identification above compiles and can be tested everywhere.
func usbDevices() []glasses.USB { return nil }
