// Package assets holds static resources embedded into the binaries so they need no
// separate install path at runtime (the system tray / app icon).
package assets

import _ "embed"

// TrayPNG is the WePapered logo sized for the system tray / app icon (128x128 PNG).
// Embedded so the tray shows our icon regardless of how the binary is installed.
//
//go:embed tray.png
var TrayPNG []byte
