// Package compositor abstracts the windowing-system specifics the wepapered
// daemon depends on, so support for new desktops/window managers is a matter of
// adding an implementation rather than threading compositor-specific calls
// through the daemon. Only Hyprland is implemented today; Detect picks it.
package compositor

import (
	"errors"
	"os"
	"strings"
)

// ErrUnsupported is returned by Detect when the current session is not a
// compositor wepapered supports (today: Hyprland on Wayland).
var ErrUnsupported = errors.New("no supported compositor detected (wepapered currently supports Hyprland on Wayland)")

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
	// Name is a short identifier, e.g. "hyprland".
	Name() string
	// Outputs returns the connected displays ordered left-to-right (by X, then Y).
	Outputs() ([]Output, error)
	// EnvOverrides returns compositor-specific environment variables a rendering
	// subprocess needs (e.g. HYPRLAND_INSTANCE_SIGNATURE). May be empty.
	EnvOverrides() map[string]string
}

// Detect returns the compositor for the current session, or ErrUnsupported when
// none is recognised (the caller should exit gracefully). The detection branches
// are the extension point for future compositors (sway, KDE/KWin, …).
func Detect() (Compositor, error) {
	switch {
	case isHyprland():
		return &Hyprland{}, nil
	default:
		return nil, ErrUnsupported
	}
}

func isHyprland() bool {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		return true
	}
	for _, v := range []string{os.Getenv("XDG_CURRENT_DESKTOP"), os.Getenv("XDG_SESSION_DESKTOP")} {
		if strings.Contains(strings.ToLower(v), "hyprland") {
			return true
		}
	}
	if entries, err := os.ReadDir(hyprDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				return true
			}
		}
	}
	return false
}
