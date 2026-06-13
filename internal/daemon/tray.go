package daemon

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"fyne.io/systray"

	"wepapered/internal/core"
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
	// Try to load the WE icon.
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

// openWEBrowser launches the wepapered-gui WebKit window. The daemon is already
// running (the tray lives in it), so the window connects straight to it.
func (t *TrayManager) openWEBrowser() {
	if bin := core.SiblingBinary(core.GUIBinary); bin != "" {
		t.launch(bin)
		return
	}
	// Fallback to a browser if the gui binary isn't shipped alongside.
	exec.Command("xdg-open", core.GUIURL(t.cfg)).Start() //nolint
}

// openConfigUI launches the wepapered-settings GTK window.
func (t *TrayManager) openConfigUI() {
	if bin := core.SiblingBinary(core.SettingsBinary); bin != "" {
		t.launch(bin)
	} else {
		log.Printf("[wepapered] settings binary (%s) not found", core.SettingsBinary)
	}
}

func (t *TrayManager) launch(bin string, args ...string) {
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[wepapered] failed to launch %s: %v", filepath.Base(bin), err)
		return
	}
	go cmd.Wait()
}

func (t *TrayManager) onExit() {
	// Signal Run() to stop everything.
	log.Println("[wepapered] Quit requested from systray")
	os.Exit(0)
}
