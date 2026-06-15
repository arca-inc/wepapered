package daemon

import "wepapered/internal/compositor"

// sys is the active compositor backend (Hyprland today). The daemon reaches the
// windowing system — enumerating outputs, the subprocess env — only through this
// interface, so supporting another desktop is a new internal/compositor
// implementation rather than changes scattered across the daemon. Set once at
// startup by Run (via compositor.Detect); nil only before then.
var sys compositor.Compositor

// hyprOutput aliases compositor.Output so the renderer's existing field types read
// unchanged. New code should use compositor.Output directly.
type hyprOutput = compositor.Output
