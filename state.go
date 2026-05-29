package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MonitorWallpaper tracks the active wallpaper per monitor.
type MonitorWallpaper struct {
	Monitor     string            `json:"monitor"`
	WinPath     string            `json:"win_path"`
	LinuxPath   string            `json:"linux_path"`
	WorkshopID  string            `json:"workshop_id"`
	Title       string            `json:"title"`
	Type        string            `json:"type"`
	PreviewFile string            `json:"preview_file"`
	DevicePath  string            `json:"device_path"` // Windows device path e.g. //?/DISPLAY#Default_Monitor#0000…
	RenderDir   string            `json:"render_dir,omitempty"`   // dependency dir (HTML/JS from framework)
	PresetDir   string            `json:"preset_dir,omitempty"`   // preset dir (assets like directories/, files/)
	Props       map[string]string `json:"props,omitempty"`        // preset property overrides
}

type DaemonState struct {
	Monitors map[string]*MonitorWallpaper `json:"monitors"` // key = Monitor0, Monitor1…
	WEPath   string                       `json:"we_path"`
}

func statePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wepapered", "state.json")
}

func loadState() *DaemonState {
	data, err := os.ReadFile(statePath())
	if err != nil {
		return &DaemonState{Monitors: make(map[string]*MonitorWallpaper)}
	}
	var s DaemonState
	if err := json.Unmarshal(data, &s); err != nil {
		return &DaemonState{Monitors: make(map[string]*MonitorWallpaper)}
	}
	if s.Monitors == nil {
		s.Monitors = make(map[string]*MonitorWallpaper)
	}
	// Upgrade: infer type and render dir for entries saved without them.
	for _, mw := range s.Monitors {
		if mw.Type == "" || mw.RenderDir == "" {
			upgradeMonitorWallpaper(mw)
		}
	}
	return &s
}

func upgradeMonitorWallpaper(mw *MonitorWallpaper) {
	meta := readProjectMeta(mw.LinuxPath)
	if meta == nil {
		return
	}
	if mw.Type == "" {
		mw.Type = meta.Type
	}
	if mw.Type == "" && meta.Dependency != "" {
		dir := mw.LinuxPath
		if !isDir(dir) {
			dir = filepath.Dir(dir)
		}
		depDir := filepath.Join(filepath.Dir(dir), meta.Dependency)
		if depMeta := readProjectMeta(filepath.Join(depDir, "project.json")); depMeta != nil && depMeta.Type != "" {
			mw.Type = depMeta.Type
			mw.RenderDir = depDir
		}
	}
}

func saveState(s *DaemonState) error {
	path := statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// winToLinux converts a WE Windows path to an absolute Linux path.
//   S: drive = steamapps root (parent of "common/wallpaper_engine")
//   Z: drive = Wine's mapping of the Linux root filesystem (Z:\home\... → /home/...)
func winToLinux(winPath, wePath string) string {
	p := strings.ReplaceAll(winPath, "\\", "/")
	// Z: = Linux root (Wine maps / to Z:\)
	if len(p) >= 3 && (p[0] == 'Z' || p[0] == 'z') && p[1] == ':' && p[2] == '/' {
		return filepath.FromSlash("/" + p[3:])
	}
	// S: = steamapps root
	steamapps := filepath.Dir(filepath.Dir(wePath)) // …/steamapps
	p = strings.TrimPrefix(p, "S:/")
	return filepath.Join(steamapps, filepath.FromSlash(p))
}

type ProjectJSON struct {
	Title      string                 `json:"title"`
	Type       string                 `json:"type"`
	File       string                 `json:"file"`
	Preview    string                 `json:"preview"`
	Dependency string                 `json:"dependency"` // workshop ID of the framework this wallpaper depends on
	Preset     map[string]interface{} `json:"preset"`     // user property overrides for dependency wallpapers
}

func readProjectMeta(linuxPath string) *ProjectJSON {
	dir := linuxPath
	if !isDir(dir) {
		dir = filepath.Dir(linuxPath)
	}
	data, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		return nil
	}
	var p ProjectJSON
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}
	if p.Type == "" {
		p.Type = inferTypeFromDir(dir)
	}
	return &p
}

// inferTypeFromDir guesses the wallpaper type from files present in the directory.
func inferTypeFromDir(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "scene.pkg")); err == nil {
		return "scene"
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		return "web"
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".mp4", ".webm", ".avi", ".mov", ".mkv":
			return "video"
		}
	}
	return ""
}

// presetToStringMap converts a mixed-type preset map to string key=value pairs
// suitable for --set-property. Skips nulls and complex types (objects, arrays).
func presetToStringMap(preset map[string]interface{}) map[string]string {
	if len(preset) == 0 {
		return nil
	}
	result := make(map[string]string, len(preset))
	for k, v := range preset {
		switch val := v.(type) {
		case nil:
			// skip
		case bool:
			if val {
				result[k] = "1"
			} else {
				result[k] = "0"
			}
		case float64:
			result[k] = strconv.FormatFloat(val, 'f', -1, 64)
		case string:
			if val != "" {
				result[k] = val
			}
		// skip arrays and objects — too complex for --set-property
		}
	}
	return result
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// workshopIDFromPath extracts the Steam workshop ID from a path.
func workshopIDFromPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if part == "431960" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
