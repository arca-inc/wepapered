package daemon

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"fyne.io/systray"

	"wepapered/assets"
	"wepapered/internal/core"
)

type TrayManager struct {
	cfg  *Config
	port int // the daemon's browse-UI port, for the xdg-open fallback URL
}

func newTrayManager(cfg *Config, port int) *TrayManager {
	return &TrayManager{cfg: cfg, port: port}
}

func (t *TrayManager) Run() {
	systray.Run(t.onReady, t.onExit)
}

func (t *TrayManager) onReady() {
	// Embedded WePapered logo (assets/tray.png) — no dependence on a runtime path.
	systray.SetIcon(assets.TrayPNG)
	systray.SetTooltip("WePapered - Wallpaper Engine for Linux")

	mBrowse := systray.AddMenuItem("Change Wallpapers...", "Open the WE UI to browse installed wallpapers")
	mConfig := systray.AddMenuItem("Configuration", "Open the WePapered configuration menu")
	mReload := systray.AddMenuItem("Reload", "Reload config and restart the wallpapers")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit WePapered and close wallpapers")

	go func() {
		for {
			select {
			case <-mBrowse.ClickedCh:
				t.openWEBrowser()
			case <-mConfig.ClickedCh:
				t.openConfigUI()
			case <-mReload.ClickedCh:
				t.reload()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

// reload asks the running daemon (this process) to re-read its config and relaunch the
// renderers, via the same local control endpoint wepaperedctl/settings use.
func (t *TrayManager) reload() {
	if err := core.ReloadDaemon(); err != nil {
		log.Printf("[wepapered] tray reload failed: %v", err)
		return
	}
	log.Printf("[wepapered] tray: reload requested")
}

// openWEBrowser launches the wepapered-gui WebKit window. The daemon is already
// running (the tray lives in it), so the window connects straight to it.
func (t *TrayManager) openWEBrowser() {
	if bin := core.SiblingBinary(core.GUIBinary); bin != "" {
		t.launch(bin)
		return
	}
	// Fallback to a browser if the gui binary isn't shipped alongside.
	exec.Command("xdg-open", core.GUIURL(t.cfg, t.port)).Start() //nolint
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
