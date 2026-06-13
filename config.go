package main

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
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wepapered", "config.json")
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return &Config{}, nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}, err
	}
	return &cfg, nil
}

func saveConfig(cfg *Config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// weDirValid reports whether p looks like a real Wallpaper Engine install.
func weDirValid(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(p, "config.json"))
	return err == nil
}

// resolveWEPath returns the configured path if it is a valid WE install,
// otherwise falls back to auto-detection. Returns "" if nothing is found.
func resolveWEPath(cfg *Config) string {
	if weDirValid(cfg.WEPath) {
		return cfg.WEPath
	}
	return autoDetectWEPath()
}

// autoDetectWEPath checks common Steam installation locations.
func autoDetectWEPath() string {
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
