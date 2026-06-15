package compositor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"wepapered/internal/core"
)

// Hyprland talks to a running Hyprland instance via hyprctl (its IPC).
type Hyprland struct{}

func (h *Hyprland) Name() string { return "hyprland" }

// EnvOverrides supplies HYPRLAND_INSTANCE_SIGNATURE so a rendering subprocess can
// reach the running compositor when our own env doesn't already carry it.
func (h *Hyprland) EnvOverrides() map[string]string {
	if sig := instanceSig(); sig != "" {
		return map[string]string{"HYPRLAND_INSTANCE_SIGNATURE": sig}
	}
	return nil
}

// Outputs enumerates monitors via `hyprctl monitors -j`, ordered left-to-right
// (by X, then Y) so the daemon can map Monitor0..N to real outputs.
func (h *Hyprland) Outputs() ([]Output, error) {
	data, err := monitorsJSON()
	if err != nil {
		return nil, err
	}
	var raw []Output
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	sort.Slice(raw, func(i, j int) bool {
		if raw[i].X != raw[j].X {
			return raw[i].X < raw[j].X
		}
		return raw[i].Y < raw[j].Y
	})
	return raw, nil
}

// monitorsJSON runs `hyprctl monitors -j`, handling the root/sudo case (reach the
// session user's Hyprland) and the normal in-session case.
func monitorsJSON() ([]byte, error) {
	if os.Getuid() == 0 {
		env := []string{
			"HOME=" + core.SessionHome(),
			"WAYLAND_DISPLAY=wayland-1",
			fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", core.SessionUID()),
			"XDG_SESSION_TYPE=wayland",
			"HYPRLAND_INSTANCE_SIGNATURE=" + instanceSig(),
		}
		args := append([]string{"-u", core.SessionUsername(), "env"},
			append(env, "hyprctl", "monitors", "-j")...)
		return exec.Command("sudo", args...).Output()
	}
	return hyprctl("monitors", "-j")
}

// hyprctl runs hyprctl against each candidate instance signature until one
// answers (covers the case where HYPRLAND_INSTANCE_SIGNATURE isn't in our env).
func hyprctl(args ...string) ([]byte, error) {
	seen := map[string]bool{}
	var sigs []string
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
		sigs = append(sigs, sig)
		seen[sig] = true
	}
	if entries, err := os.ReadDir(hyprDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				sigs = append(sigs, e.Name())
				seen[e.Name()] = true
			}
		}
	}
	if len(sigs) == 0 {
		return nil, fmt.Errorf("no Hyprland instances found")
	}
	baseEnv := core.WaylandSessionEnv(nil)
	var lastErr error
	for _, sig := range sigs {
		cmd := exec.Command("hyprctl", append([]string{"-i", sig}, args...)...)
		cmd.Env = append(append([]string(nil), baseEnv...), "HYPRLAND_INSTANCE_SIGNATURE="+sig)
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// instanceSig returns the first Hyprland instance signature in the session
// runtime dir, or "".
func instanceSig() string {
	entries, _ := os.ReadDir(hyprDir())
	for _, e := range entries {
		if e.IsDir() {
			return e.Name()
		}
	}
	return ""
}

// hyprDir is the Hyprland socket directory for the session.
func hyprDir() string {
	return filepath.Join(core.SessionRuntimeDir(), "hypr")
}
