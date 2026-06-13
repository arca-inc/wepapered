package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"fyne.io/systray"
)

type TrayManager struct {
	cfg *Config
}

func newTrayManager(cfg *Config) *TrayManager {
	return &TrayManager{cfg: cfg}
}

func (t *TrayManager) Run() {
	systray.Run(t.onReady, t.onExit)
}

func (t *TrayManager) onReady() {
	// Try to load WE icon
	iconPath := filepath.Join(t.cfg.WEPath, "ui", "dist", "favicon.ico")
	if iconData, err := os.ReadFile(iconPath); err == nil {
		systray.SetIcon(iconData)
	} else {
		systray.SetTitle("WE")
	}

	systray.SetTooltip("WePapered - Wallpaper Engine for Linux")

	mBrowse := systray.AddMenuItem("Change Wallpapers...", "Open the WE UI to browse installed wallpapers")
	mConfig := systray.AddMenuItem("Configuration", "Open the WePapered configuration menu")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit WePapered and close wallpapers")

	go func() {
		for {
			select {
			case <-mBrowse.ClickedCh:
				t.openWEBrowser()
			case <-mConfig.ClickedCh:
				t.openConfigUI()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func (t *TrayManager) openWEBrowser() {
	// Use the wepapered-ui companion binary: a frameless WebKitGTK window,
	// no browser chrome, no address bar — Electron-like feel. The daemon is
	// already running (the tray lives in it), so just open the window.
	url := guiURL(t.cfg)
	bin := findCompanion()
	if bin == "" {
		// Fallback to a browser if the companion binary isn't shipped alongside.
		exec.Command("xdg-open", url).Start() //nolint
		return
	}
	cmd := exec.Command(bin, url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[wepapered] Failed to open WE browser UI: %v", err)
	} else {
		go cmd.Wait()
	}
}

func (t *TrayManager) openConfigUI() {
	// WePapered GTK configuration UI is triggered by running the binary with --ui
	exe, err := os.Executable()
	if err != nil {
		log.Printf("[wepapered] Could not find own executable: %v", err)
		return
	}
	cmd := exec.Command(exe, "--config")
	if err := cmd.Start(); err != nil {
		log.Printf("[wepapered] Failed to start config UI: %v", err)
	} else {
		go cmd.Wait()
	}
}

func (t *TrayManager) onExit() {
	// Signal main.go to stop everything
	log.Println("[wepapered] Quit requested from systray")
	os.Exit(0)
}
