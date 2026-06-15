// Package compositor abstracts the windowing-system specifics the wepapered
// daemon depends on, so support for new desktops/window managers is a matter of
// adding an implementation rather than threading compositor-specific calls
// through the daemon. It currently utilizes a native Wayland client adapter.
package compositor

import (
	"errors"
	"os"
)

// ErrUnsupported is returned by Detect when the current session is not a
// supported environment (today: Wayland native).
var ErrUnsupported = errors.New("no supported compositor detected (wepapered requires a Wayland session)")

// Output is one connected display, in a compositor-independent form. Geometry is
// in layout pixels (the global coordinate space the compositor lays monitors out
// in), so outputs can be ordered left-to-right / top-to-bottom.
type Output struct {
	Name   string `json:"name"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Compositor is everything the daemon needs from the windowing system: which
// displays exist (to map MonitorN labels to real outputs and to fill the UI's
// display picker) and the extra environment a rendering subprocess needs to reach
// the session. Implementations must be safe for concurrent use.
type Compositor interface {
	// Name is a short identifier, e.g. "wayland-native".
	Name() string
	// Outputs returns the connected displays ordered left-to-right (by X, then Y).
	Outputs() ([]Output, error)
	// EnvOverrides returns compositor-specific environment variables a rendering
	// subprocess needs. May be empty.
	EnvOverrides() map[string]string
}

// Detect returns the compositor for the current session.
// It relies completely on the Wayland native API for output discovery.
func Detect() (Compositor, error) {
	if isWayland() {
		return &Wayland{}, nil
	}
	return nil, ErrUnsupported
}

func isWayland() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return true
	}
	return false
}
