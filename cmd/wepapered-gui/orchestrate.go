package main

import (
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"wepapered/internal/core"
)

// ensureDaemon makes sure the wepapered daemon is up before the browse window
// opens (the window is useless without the WS server it talks to). If nothing is
// listening on the control port it starts the daemon binary detached, then waits
// for it to come up.
func ensureDaemon() {
	if daemonReachable() {
		return
	}
	log.Println("[wepapered-gui] no daemon on 127.0.0.1:9001 — starting it")
	if err := startDaemonDetached(); err != nil {
		log.Printf("[wepapered-gui] could not start daemon: %v", err)
	}
	waitForDaemon(8 * time.Second)
}

// daemonReachable reports whether something is listening on the daemon's WS port.
func daemonReachable() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9001", 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// startDaemonDetached launches the wepapered-daemon binary in a new session, so
// the daemon (and the wallpapers it renders) outlives this GUI window. Its output
// goes to a log file (not our terminal) and its stdin is detached, so the spawn
// is silent and fully decoupled from the GUI process.
func startDaemonDetached() error {
	bin := core.SiblingBinary(core.DaemonBinary)
	if bin == "" {
		return os.ErrNotExist
	}
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if devnull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0); err == nil {
		cmd.Stdin = devnull
	}
	logPath := filepath.Join(filepath.Dir(core.ConfigPath()), "daemon.log")
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
	} else if dn, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		cmd.Stdout = dn
		cmd.Stderr = dn
	}
	return cmd.Start()
}

// waitForDaemon polls until the daemon's WS port accepts connections or timeout.
func waitForDaemon(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if daemonReachable() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	log.Println("[wepapered-gui] daemon did not come up in time; opening window anyway")
}
