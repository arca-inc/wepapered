// wepapered-daemon — the background service. Bridges Wallpaper Engine (run under
// Proton) to linux-wallpaperengine, rendering one LWE subprocess per output,
// serving the browse UI on a random local port (advertised via the Unix control
// socket), and running the system tray.
//
// Flags: --dump-library prints the enumerated wallpaper library as JSON and exits.
package main

import "wepapered/internal/daemon"

func main() {
	daemon.Run()
}
