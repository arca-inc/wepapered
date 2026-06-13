// wepaperedctl — minimal CLI dispatcher. It locates the wepapered component
// binaries that sit alongside it (or on PATH) and execs the requested one,
// forwarding any extra arguments. It contains no logic of its own.
package main

import (
	"fmt"
	"os"
	"syscall"

	"wepapered/internal/core"
)

var components = map[string]string{
	"daemon":   core.DaemonBinary,
	"gui":      core.GUIBinary,
	"settings": core.SettingsBinary,
}

func usage() {
	fmt.Fprint(os.Stderr, `wepaperedctl — control wepapered

usage: wepaperedctl <command> [args...]

commands:
  daemon      run the background renderer/daemon
  gui         open the Wallpaper Engine browse window
  settings    open the settings window
  help        show this help

Extra args are forwarded to the component
(e.g. "wepaperedctl daemon --dump-library").
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		usage()
		return
	}

	bin, ok := components[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "wepaperedctl: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	path := core.SiblingBinary(bin)
	if path == "" {
		fmt.Fprintf(os.Stderr, "wepaperedctl: %q not found next to wepaperedctl or on PATH\n", bin)
		os.Exit(1)
	}

	// Replace this process with the target so its exit code and signals pass
	// straight through — wepaperedctl adds nothing at runtime.
	argv := append([]string{path}, os.Args[2:]...)
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "wepaperedctl: exec %s: %v\n", path, err)
		os.Exit(1)
	}
}
