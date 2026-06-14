// Package core holds the pure-Go state shared by every wepapered binary: the
// on-disk config, Wallpaper Engine path resolution, companion-binary location,
// and the browse-UI URL. It links no CGo and pulls no heavy dependencies, so the
// daemon (LWE), the GTK settings window, the WebKit browse window, and the ctl
// dispatcher can all import it without dragging in each other's native libraries.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	WEPath      string            `json:"we_path"`
	SteamAPIKey string            `json:"steam_api_key"`
	Assignments map[string]string `json:"assignments"`
	// CustomDirs are extra directories to scan for manually-downloaded
	// wallpapers. Each entry may be a single wallpaper directory (one holding a
	// project.json) or a parent directory containing many such subdirectories.
	CustomDirs []string `json:"custom_dirs"`
	// GuiSkin selects the WE UI theme for the native window, by stylesheet base
	// name under ui/dist/styles (without the .css). Defaults to "skindark".
	// Other shipped options: skinobsidian, skinspace, skinmetal, skinmist,
	// skinmoss, skinrose, skinrust, skinwinter, skinhalloween, main (light).
	GuiSkin string `json:"gui_skin"`
	// PlaceholderBackend chooses the tool that paints the loading placeholder
	// image on each output while LWE starts up: "hyprpaper" (default), "swww",
	// "none" (no placeholder), or a custom command template where {output} and
	// {image} are substituted (e.g. "swww img {image} --outputs {output}").
	PlaceholderBackend string `json:"placeholder_backend"`
	// AudioDevice forces which audio source the visualizer reacts to (a PulseAudio/
	// PipeWire source name, usually a "<sink>.monitor"). Empty = follow the default
	// output sink's monitor automatically. Passed to LWE as LWE_AUDIO_DEVICE.
	AudioDevice string `json:"audio_device"`
	// NowPlayingText, when true, pushes the current track's title/artist (from MPRIS
	// via playerctl) into a web wallpaper's headerText / subheaderText properties.
	// Lets wallpapers that show a text label (e.g. audio visualizers) display the
	// now-playing track without their own cloud integration. Off by default because
	// it overrides those text fields whenever something is playing. Passed to LWE as
	// LWE_MEDIA_TO_TEXT.
	NowPlayingText bool `json:"now_playing_text"`
	// MediaPlayer is the preferred MPRIS player priority list for now-playing data,
	// forwarded to `playerctl --player=` (e.g. "spotify,%any" — prefer Spotify, fall
	// back to any). Empty = playerctl's default selection. Passed as LWE_MEDIA_PLAYER.
	MediaPlayer string `json:"media_player"`
}

func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wepapered", "config.json")
}

func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return &Config{}, nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WeDirValid reports whether p looks like a real Wallpaper Engine install.
func WeDirValid(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(p, "config.json"))
	return err == nil
}

// ResolveWEPath returns the configured path if it is a valid WE install,
// otherwise falls back to auto-detection. Returns "" if nothing is found.
func ResolveWEPath(cfg *Config) string {
	if WeDirValid(cfg.WEPath) {
		return cfg.WEPath
	}
	return AutoDetectWEPath()
}

// AutoDetectWEPath checks common Steam installation locations.
func AutoDetectWEPath() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam/steamapps/common/wallpaper_engine"),
		filepath.Join(home, ".local/share/Steam/steamapps/common/wallpaper_engine"),
		"/mnt/sata/SteamLibrary/steamapps/common/wallpaper_engine",
		"/mnt/nvme/SteamLibrary/steamapps/common/wallpaper_engine",
	}
	for _, p := range candidates {
		if _, err := os.Stat(filepath.Join(p, "config.json")); err == nil {
			return p
		}
	}
	return ""
}
