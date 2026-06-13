package main

/*
#cgo CFLAGS: -I${SRCDIR}/lwe/src
#cgo LDFLAGS: -L${SRCDIR}/lwe/build/output -llinux-wallpaperengine-lib -Wl,-rpath,$ORIGIN -Wl,-rpath,${SRCDIR}/lwe/build/output
#include "lwe_bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"os"
	"path/filepath"
	"unsafe"
)

// lweOutputDir is the directory containing the built LWE binaries.
// Override with LWE_OUTPUT_DIR env var; defaults to the submodule build output.
var lweOutputDir = func() string {
	if d := os.Getenv("LWE_OUTPUT_DIR"); d != "" {
		return d
	}
	// Packaged install: LWE binaries next to the wepapered executable.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "linux-wallpaperengine")); err == nil {
			return dir
		}
		// Development default: submodule build output next to the executable.
		if _, err := os.Stat(filepath.Join(dir, "lwe/build/output", "linux-wallpaperengine")); err == nil {
			return filepath.Join(dir, "lwe/build/output")
		}
	}
	// Fallback.
	return filepath.Join(os.Getenv("HOME"), "wepapered/lwe/build/output")
}()

var lwebin = filepath.Join(lweOutputDir, "linux-wallpaperengine")

// lwesubprocessbin is the minimal CEF subprocess helper (only calls CefExecuteProcess).
// Using this instead of the full LWE binary avoids it trying to init Wayland when
// CEF spawns it with --type=renderer, which caused a deadlock.
var lwesubprocessbin = filepath.Join(lweOutputDir, "lwe-cef-subprocess")

func lweSetSubprocessPath(path string) {
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	C.lwe_set_subprocess_path(cs)
}
