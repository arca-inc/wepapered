package core

import (
	"os"
	"os/exec"
	"path/filepath"
)

// LWEOutputDir is the directory containing the built LWE binaries
// (linux-wallpaperengine, lwe-cef-subprocess) and the bundled shared libs.
// Override with LWE_OUTPUT_DIR; defaults to the submodule build output found
// next to the running executable.
var LWEOutputDir = func() string {
	if d := os.Getenv("LWE_OUTPUT_DIR"); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		// Packaged install: LWE binaries sit right next to the executable.
		if FileExists(filepath.Join(dir, "linux-wallpaperengine")) {
			return dir
		}
		// Development: the submodule build output (lwe/build/output), reached from
		// the executable's directory or its parent — the latter covers binaries
		// built into ./bin while the lib stays in ./lwe/build/output.
		for _, root := range []string{dir, filepath.Dir(dir)} {
			out := filepath.Join(root, "lwe", "build", "output")
			if FileExists(filepath.Join(out, "linux-wallpaperengine")) {
				return out
			}
		}
	}
	return filepath.Join(os.Getenv("HOME"), "wepapered/lwe/build/output")
}()

// LWEBin is the linux-wallpaperengine renderer binary.
var LWEBin = filepath.Join(LWEOutputDir, "linux-wallpaperengine")

// LWESubprocessBin is the minimal CEF subprocess helper (only calls
// CefExecuteProcess). Using this instead of the full LWE binary avoids it trying
// to init Wayland when CEF spawns it with --type=renderer, which deadlocks.
var LWESubprocessBin = filepath.Join(LWEOutputDir, "lwe-cef-subprocess")

// FileExists reports whether p exists.
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// SiblingBinary locates a companion wepapered binary by name: next to the
// running executable, then in the LWE output dir, then on PATH. Returns "" if
// none is found. Used to launch the daemon/gui/settings binaries from one
// another without hard-coding install paths.
func SiblingBinary(name string) string {
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), name); FileExists(p) {
			return p
		}
	}
	if p := filepath.Join(LWEOutputDir, name); FileExists(p) {
		return p
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}
