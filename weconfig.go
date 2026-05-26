package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

// weConfigPath is the path to WE's config.json.
func weConfigPath(wePath string) string {
	return wePath + "/config.json"
}

// MonitorInfo holds the Windows device path for a monitor at a given location index.
type MonitorInfo struct {
	DevicePath string // e.g. //?/DISPLAY#Default_Monitor#0000&…
	Location   int    // 0-based index matching Monitor0, Monitor1…
}

// parseMonitorMap extracts the monitor device paths from applyGeneral's monitormap payload.
// monitormap format: { "<devicePath>": {"location": 0, ...}, ... }
func parseMonitorMap(raw interface{}) []MonitorInfo {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	infos := make([]MonitorInfo, 0, len(m))
	for devPath, v := range m {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		loc := 0
		if l, ok := entry["location"].(float64); ok {
			loc = int(l)
		}
		infos = append(infos, MonitorInfo{DevicePath: devPath, Location: loc})
	}
	return infos
}

// monitorIndexLabel returns "Monitor0", "Monitor1", … for a location index.
func monitorIndexLabel(loc int) string {
	return fmt.Sprintf("Monitor%d", loc)
}

// writeWESelectedWallpapers persists the selected wallpapers back into WE's config.json
// so that WE remembers them on next startup.
func writeWESelectedWallpapers(wePath string, monitors map[string]*MonitorWallpaper, monitorInfos []MonitorInfo) error {
	cfgPath := weConfigPath(wePath)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	// Build a location→devicePath lookup.
	locToDevice := make(map[int]string)
	for _, mi := range monitorInfos {
		locToDevice[mi.Location] = mi.DevicePath
	}

	// Build selectedwallpapers map.
	// Key: Windows device path (what WE uses internally) OR MonitorN label as fallback.
	selected := make(map[string]interface{})
	for label, mw := range monitors {
		// Parse location from "Monitor0" → 0
		loc := -1
		fmt.Sscanf(label, "Monitor%d", &loc)

		key := label // fallback
		if loc >= 0 {
			if dev, ok := locToDevice[loc]; ok {
				key = dev
			}
		}

		entry := map[string]interface{}{
			"file": mw.WinPath,
			"type": mw.Type,
		}
		if mw.WorkshopID != "" {
			entry["workshopid"] = mw.WorkshopID
		}
		selected[key] = entry

		// Also store by MonitorN label so WE can find it either way.
		if key != label {
			selected[label] = entry
		}
	}

	// Navigate to steamuser.general.wallpaperconfig
	su, _ := cfg["steamuser"].(map[string]interface{})
	if su == nil {
		return fmt.Errorf("steamuser missing")
	}
	gen, _ := su["general"].(map[string]interface{})
	if gen == nil {
		return fmt.Errorf("general missing")
	}
	wc, _ := gen["wallpaperconfig"].(map[string]interface{})
	if wc == nil {
		wc = make(map[string]interface{})
		gen["wallpaperconfig"] = wc
	}
	wc["selectedwallpapers"] = selected

	out, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, out, 0644); err != nil {
		return err
	}
	log.Printf("[wepapered] wrote %d wallpaper(s) to WE config.json", len(monitors))
	return nil
}
