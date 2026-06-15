// wepaperedctl — minimal CLI dispatcher. It locates the wepapered component
// binaries that sit alongside it (or on PATH) and execs the requested one,
// forwarding any extra arguments. The one bit of logic it adds: a plain
// `wepaperedctl daemon` detaches the daemon into the background (no need for
// `& disown`).
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
  daemon      start the renderer/daemon in the background (detached)
  gui         open the Wallpaper Engine browse window
  settings    open the settings window
  reload      tell a running daemon to reload config + restart renderers
  stop        stop the running daemon (and its wallpapers) gracefully
  version     print the build version
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
	case "version", "--version", "-v":
		fmt.Println(core.VersionString())
		return
	case "reload":
		if err := core.ReloadDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "wepaperedctl: reload failed (is the daemon running?): %v\n", err)
			os.Exit(1)
		}
		fmt.Println("wepapered: daemon reloaded")
		return
	case "stop":
		if err := core.StopDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "wepaperedctl: stop failed (is the daemon running?): %v\n", err)
			os.Exit(1)
		}
		fmt.Println("wepapered: daemon stopped")
		return
	}

	// Plain `wepaperedctl daemon` starts the daemon detached in the background so
	// the terminal is freed (no `& disown` needed). With extra args (e.g.
	// --dump-library) it falls through to a normal foreground exec so output and
	// exit code pass through.
	if os.Args[1] == "daemon" && len(os.Args) == 2 {
		startDaemonDetached()
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

// startDaemonDetached launches the daemon in its own session (detached from this
// terminal, so it survives the shell closing) with its output appended to a log
// file, then returns. The daemon's own single-instance gate handles a duplicate,
// but we check first for a friendly message.
func startDaemonDetached() {
	if core.DaemonReachable() {
		fmt.Println("wepapered: daemon already running")
		return
	}
	path := core.SiblingBinary(core.DaemonBinary)
	if path == "" {
		fmt.Fprintf(os.Stderr, "wepaperedctl: %q not found next to wepaperedctl or on PATH\n", core.DaemonBinary)
		os.Exit(1)
	}

	logPath := daemonLogPath()
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)

	cmd := exec.Command(path)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	if logFile != nil {
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	// New session → detached from the controlling terminal, no SIGHUP on close.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "wepaperedctl: failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	if logFile != nil {
		logFile.Close() // the child holds its own copy of the fd
	}
	if logPath != "" {
		fmt.Printf("wepapered: daemon started (pid %d) — logs: %s\n", cmd.Process.Pid, logPath)
	} else {
		fmt.Printf("wepapered: daemon started (pid %d)\n", cmd.Process.Pid)
	}
}

// daemonLogPath is ~/.config/wepapered/daemon.log (the dir is created if needed).
// Returns "" if the home dir can't be resolved (the daemon then inherits no log
// file and writes nowhere — still detached).
func daemonLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := filepath.Join(home, ".config", "wepapered")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return filepath.Join(dir, "daemon.log")
}
