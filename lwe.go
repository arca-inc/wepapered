package main

/*
#cgo CFLAGS: -I${SRCDIR}/lwe/src
#cgo LDFLAGS: -L${SRCDIR}/lwe/build/output -llinux-wallpaperengine-lib -Wl,-rpath,${SRCDIR}/lwe/build/output
#include "lwe_bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"os"
	"path/filepath"
	"runtime"
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
	}
	// Development default: submodule build output relative to HOME.
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

// lweRunAsync starts LWE with args in a dedicated OS-locked goroutine.
// The returned channel is closed when LWE exits.
// NOTE: not used for rendering in subprocess mode — kept for CGo embedded mode.
func lweRunAsync(args []string) chan struct{} {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)

		argc := C.int(len(args))
		argv := make([]*C.char, len(args))
		for i, a := range args {
			argv[i] = C.CString(a)
		}
		defer func() {
			for _, p := range argv {
				C.free(unsafe.Pointer(p))
			}
		}()
		C.lwe_run(argc, &argv[0])
	}()
	return done
}

func lweStop() {
	C.lwe_stop()
}
