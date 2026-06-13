package core

// Binary names of the wepapered components, used with SiblingBinary to launch
// one from another.
const (
	DaemonBinary   = "wepapered-daemon"
	GUIBinary      = "wepapered-gui"
	SettingsBinary = "wepapered-settings"
)

// GUIURL builds the hosted WE browse UI URL with the configured skin (dark by
// default). index.html reads the skinStyle query param and loads that stylesheet
// from ui/dist/styles, so the theme is just the CSS file selected here. The
// daemon serves this URL on 127.0.0.1:9001.
func GUIURL(cfg *Config) string {
	skin := cfg.GuiSkin
	if skin == "" {
		skin = "skindark"
	}
	return "http://localhost:9001/ui/index.html?skinStyle=styles/" + skin +
		".css&skinKey=" + skin + "&cb=1#/browsewallpapers"
}
