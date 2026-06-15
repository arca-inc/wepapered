package core

import (
	"fmt"
	"os"
	"strings"
)

// Session helpers identify the owner of the graphical session. When wepapered
// runs as root (e.g. via sudo) the SUDO_* variables point back at the real user;
// the embedded renderer/IPC must reach that user's Wayland session, not root's.
// These are compositor-independent (any Wayland session).

// SessionUID is the UID of the graphical-session owner.
func SessionUID() int {
	if os.Getuid() == 0 {
		if s := os.Getenv("SUDO_UID"); s != "" {
			var uid int
			fmt.Sscan(s, &uid)
			if uid > 0 {
				return uid
			}
		}
	}
	return os.Getuid()
}

// SessionUsername is the login name of the graphical-session owner.
func SessionUsername() string {
	if os.Getuid() == 0 {
		if u := os.Getenv("SUDO_USER"); u != "" {
			return u
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

// SessionHome is the home directory of the graphical-session owner.
func SessionHome() string {
	if os.Getuid() == 0 {
		if u := os.Getenv("SUDO_USER"); u != "" {
			return "/home/" + u
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/root"
}

// SessionRuntimeDir is the session owner's XDG_RUNTIME_DIR (or the conventional
// /run/user/<uid> when unset).
func SessionRuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return fmt.Sprintf("/run/user/%d", SessionUID())
}

// WaylandSessionEnv returns the current environment with the Wayland session
// variables ensured — XDG_SESSION_TYPE=wayland, plus WAYLAND_DISPLAY and
// XDG_RUNTIME_DIR defaulted when unset — then the given overrides layered on top
// (overrides win). Used to build the environment for subprocesses that must reach
// the session's Wayland compositor (the renderer's LWE processes, hyprctl, …).
func WaylandSessionEnv(overrides map[string]string) []string {
	merged := map[string]string{"XDG_SESSION_TYPE": "wayland"}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		merged["WAYLAND_DISPLAY"] = "wayland-1"
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		merged["XDG_RUNTIME_DIR"] = SessionRuntimeDir()
	}
	for k, v := range overrides {
		merged[k] = v
	}
	result := make([]string, 0, len(os.Environ())+len(merged))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			if _, skip := merged[kv[:i]]; skip {
				continue
			}
		}
		result = append(result, kv)
	}
	for k, v := range merged {
		result = append(result, k+"="+v)
	}
	return result
}
