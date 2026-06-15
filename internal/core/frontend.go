package core

import "fmt"

// Binary names of the wepapered components, used with SiblingBinary to launch
// one from another.
const (
	DaemonBinary   = "wepapered-daemon"
	GUIBinary      = "wepapered-gui"
	SettingsBinary = "wepapered-settings"
)

// GUIURL builds the hosted WE browse UI URL with the configured skin (dark by
// default). index.html reads the skinStyle query param and loads that stylesheet
// from ui/dist/styles, so the theme is just the CSS file selected here. The daemon
// serves this on a random loopback port (port), discovered via DaemonPort.
func GUIURL(cfg *Config, port int) string {
	skin := cfg.GuiSkin
	if skin == "" {
		skin = "skindark"
	}
	return fmt.Sprintf("http://127.0.0.1:%d/ui/index.html?skinStyle=styles/%s.css&skinKey=%s&cb=1#/browsewallpapers",
		port, skin, skin)
}
