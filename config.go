package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	WEPath string `json:"we_path"`
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
