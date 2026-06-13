package main

import (
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// guiURL builds the hosted WE browse UI URL with the configured skin (dark by
// default). index.html reads the skinStyle query param and loads that stylesheet
// from ui/dist/styles, so the theme is just the CSS file selected here.
func guiURL(cfg *Config) string {
	skin := cfg.GuiSkin
	if skin == "" {
		skin = "skindark"
	}
	return "http://localhost:9001/ui/index.html?skinStyle=styles/" + skin +
		".css&skinKey=" + skin + "&cb=1#/browsewallpapers"
}

// runGUI opens the native WebKitGTK browse window (the wepapered-ui companion),
// starting the background daemon first if it isn't already running.
//
// The window lives in its OWN process on purpose: it forces the X11/XWayland
// backend and unsets WAYLAND_DISPLAY (webkit2gtk crashes on raw Wayland), and it
// links webkit2gtk — neither of which can share the daemon process, which needs
// the Wayland environment for rendering and already links libcef via the LWE
// library. runGUI therefore only orchestrates: ensure daemon, launch window.
func runGUI(cfg *Config) {
	if !daemonReachable() {
		log.Println("[wepapered] no daemon on 127.0.0.1:9001 — starting it")
		if err := startDaemonDetached(); err != nil {
			log.Printf("[wepapered] could not start daemon: %v", err)
		}
		waitForDaemon(8 * time.Second)
	}

	url := guiURL(cfg)
	bin := findCompanion()
	if bin == "" {
		log.Println("[wepapered] wepapered-ui window binary not found — opening in default browser")
		exec.Command("xdg-open", url).Start() //nolint
		return
	}

	cmd := exec.Command(bin, url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[wepapered] failed to open GUI window: %v", err)
		return
	}
	cmd.Wait() // block until the window is closed, like a normal app
}

// findCompanion locates the wepapered-ui window binary: next to this executable,
// then in the LWE output dir, then on PATH.
func findCompanion() string {
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "wepapered-ui"); fileExists(p) {
			return p
		}
	}
	if p := filepath.Join(lweOutputDir, "wepapered-ui"); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("wepapered-ui"); err == nil {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
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

// startDaemonDetached launches this binary in daemon mode in a new session, so
// the daemon (and the wallpapers it renders) outlives the GUI window. Its env is
// inherited clean — the GUI window mangles only its own process env.
func startDaemonDetached() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		cmd.Stdout = devnull
		cmd.Stderr = devnull
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
	log.Println("[wepapered] daemon did not come up in time; opening window anyway")
}
