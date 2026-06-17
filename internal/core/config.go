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
	"regexp"
	"strings"
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
	// Favorites are the wallpaper identity keys (UIWallpaper.File) the user has
	// favorited in the WE UI (heart). Persisted so favorites survive restarts.
	Favorites []string `json:"favorites,omitempty"`
	// FPS is the target frame rate passed to LWE (--fps). Default 30. Set to
	// 60 for 60 Hz monitors or higher for high-refresh displays. Values above
	// the monitor's refresh rate are pointless but harmless.
	FPS int `json:"fps,omitempty"`
	// FetchAlbumArt, when enabled, runs an MPRIS proxy player that mirrors the active
	// player and resolves the album art (YouTube thumbnail from the track URL, or
	// the iTunes Search API by artist+title) when the real player exposes none —
	// so now-playing wallpapers show cover art even with players like Firefox that
	// don't provide mpris:artUrl. LWE is pointed at the proxy via LWE_MEDIA_PLAYER.
	// Pointer so absent (older configs) defaults to ON; use AlbumArtEnabled().
	FetchAlbumArt *bool `json:"fetch_album_art,omitempty"`
}

// AlbumArtEnabled reports whether the album-art MPRIS proxy should run. Defaults
// to true when unset (the field is opt-out, checked-by-default in the settings UI).
func (c *Config) AlbumArtEnabled() bool {
	return c.FetchAlbumArt == nil || *c.FetchAlbumArt
}

func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wepapered", "config.json")
}

// DefaultConfig returns a fresh config populated with sensible defaults: the
// auto-detected Wallpaper Engine path, the default UI skin, and an empty
// assignment map. Used on first run when no config file exists yet.
func DefaultConfig() *Config {
	return &Config{
		WEPath:      AutoDetectWEPath(),
		GuiSkin:     "skindark",
		Assignments: map[string]string{},
		FPS:         30,
	}
}

func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		// First run: no config yet — create it on disk populated with defaults so
		// the user has a real, editable file (with the detected WE path) from the
		// start. Any other read error (e.g. permissions) returns empty without
		// clobbering whatever is there.
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			_ = SaveConfig(cfg) // best-effort; ok if the dir isn't writable yet
			return cfg, nil
		}
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

// WeDirValid reports whether p looks like a real Wallpaper Engine install,
// identified by the wallpaper32.exe + wallpaper64.exe signature (same check as
// autodetect). Note: WE's own config.json is runtime state that may not exist
// until WE has run, so it is NOT a reliable install marker.
func WeDirValid(p string) bool {
	return p != "" && isWEDir(p)
}

// ResolveWEPath returns the configured path if it is a valid WE install,
// otherwise falls back to auto-detection. Returns "" if nothing is found.
func ResolveWEPath(cfg *Config) string {
	if WeDirValid(cfg.WEPath) {
		return cfg.WEPath
	}
	return AutoDetectWEPath()
}

// isWEDir reports whether dir holds a Wallpaper Engine install, identified by the
// presence of BOTH wallpaper32.exe and wallpaper64.exe — the signature we use to
// recognise WE regardless of how/where it was installed.
func isWEDir(dir string) bool {
	for _, exe := range []string{"wallpaper32.exe", "wallpaper64.exe"} {
		if _, err := os.Stat(filepath.Join(dir, exe)); err != nil {
			return false
		}
	}
	return true
}

// AutoDetectWEPath finds the Wallpaper Engine install by looking for a directory
// containing wallpaper32.exe + wallpaper64.exe in known locations: every Steam
// library (default installs + libraryfolders.vdf, across all drives) under
// steamapps/common/wallpaper_engine, plus common SteamLibrary folders and manual
// install spots on mounted drives so a copy installed outside Steam is still found.
func AutoDetectWEPath() string {
	var cands []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		cands = append(cands, p)
	}

	// Registered Steam libraries (default installs + libraryfolders.vdf on any disk).
	for _, lib := range SteamLibraryDirs() {
		add(filepath.Join(lib, "steamapps", "common", "wallpaper_engine"))
	}

	// Mounted drives: SteamLibrary folders that may not be registered in
	// libraryfolders.vdf yet, plus installs outside Steam entirely.
	for _, pat := range []string{"/mnt/*", "/media/*", "/media/*/*", "/run/media/*/*"} {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			add(filepath.Join(m, "SteamLibrary", "steamapps", "common", "wallpaper_engine"))
			add(filepath.Join(m, "steamapps", "common", "wallpaper_engine"))
			add(filepath.Join(m, "wallpaper_engine"))
			add(filepath.Join(m, "Wallpaper Engine"))
		}
	}

	for _, c := range cands {
		if isWEDir(c) {
			return c
		}
	}
	return ""
}

var libPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)

// SteamLibraryDirs returns the Steam library root directories, parsed from
// libraryfolders.vdf, always including the default native and Flatpak installs.
func SteamLibraryDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		// Canonicalize so symlinked roots (e.g. ~/.steam/steam -> ~/.local/share/
		// Steam) collapse to one entry instead of producing duplicate library rows.
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			p = rp
		}
		if seen[p] {
			return
		}
		seen[p] = true
		dirs = append(dirs, p)
	}

	bases := []string{
		filepath.Join(home, ".local", "share", "Steam"),
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
	}
	for _, b := range bases {
		add(b)
	}
	for _, b := range bases {
		data, err := os.ReadFile(filepath.Join(b, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range libPathRe.FindAllStringSubmatch(string(data), -1) {
			add(filepath.FromSlash(strings.ReplaceAll(m[1], `\\`, `/`)))
		}
	}
	return dirs
}
