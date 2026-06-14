package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	fsw     *fsnotify.Watcher
	done    chan struct{}
	ws      *WSServer
	mu      sync.Mutex
	wepath  string // WE install path; guarded by mu (Rebind updates it on reload)
	target  string // currently watched config.json path; guarded by mu
	reapply *time.Timer
}

func newWatcher(cfg *Config, ws *WSServer) *Watcher {
	return &Watcher{
		wepath: cfg.WEPath,
		done:   make(chan struct{}),
		ws:     ws,
	}
}

func (w *Watcher) Start() error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.fsw = fsw

	w.mu.Lock()
	w.target = filepath.Join(w.wepath, "config.json")
	dir := w.wepath
	w.mu.Unlock()

	// Watch the install DIRECTORY, not config.json directly: WE's config.json may
	// not exist yet (WE never configured) and WE rewrites it via atomic rename,
	// which detaches a file-level watch. Watching the dir catches the file being
	// created/replaced; events are filtered down to config.json below.
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return fmt.Errorf("cannot watch %s: %w", dir, err)
	}
	log.Printf("[wepapered] watching: %s", filepath.Join(dir, "config.json"))

	go func() {
		for {
			select {
			case event, ok := <-fsw.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) != "config.json" {
					continue
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
					w.handleChange(w.currentTarget())
				}
			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				log.Printf("[wepapered] watch error: %v", err)
			case <-w.done:
				return
			}
		}
	}()
	return nil
}

func (w *Watcher) currentTarget() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.target
}

// Rebind re-points the watch at a new WE install's config.json after a daemon reload
// that changed the WE path. No-op when the path is unchanged. Best-effort: if the new
// config.json can't be watched yet, it logs and keeps the previous watch, but still
// updates wepath so reapply writes go to the new install.
func (w *Watcher) Rebind(newWEPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	newTarget := filepath.Join(newWEPath, "config.json")
	if newWEPath == w.wepath {
		return
	}
	oldDir := w.wepath
	if w.fsw == nil {
		w.wepath, w.target = newWEPath, newTarget
		return
	}
	if oldDir != "" {
		w.fsw.Remove(oldDir) //nolint:errcheck
	}
	if err := w.fsw.Add(newWEPath); err != nil {
		log.Printf("[wepapered] reload: cannot watch %s (keeping previous watch): %v", newWEPath, err)
		w.wepath, w.target = newWEPath, newTarget
		return
	}
	w.wepath, w.target = newWEPath, newTarget
	log.Printf("[wepapered] watch re-pointed to: %s", newTarget)
}

func (w *Watcher) Stop() {
	close(w.done)
	if w.fsw != nil {
		w.fsw.Close()
	}
}

func (w *Watcher) handleChange(path string) {
	// Check if WE cleared our selectedwallpapers.
	if w.ws != nil && len(w.ws.state.Monitors) > 0 {
		if weCleared(path) {
			log.Printf("[wepapered] WE cleared selectedwallpapers — scheduling reapply")
			w.scheduleReapply()
			return
		}
	}
	log.Printf("[wepapered] WE config updated")
}

// weCleared returns true if selectedwallpapers in the file is empty.
func weCleared(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg struct {
		SteamUser struct {
			General struct {
				WallpaperConfig struct {
					SelectedWallpapers map[string]interface{} `json:"selectedwallpapers"`
				} `json:"wallpaperconfig"`
			} `json:"general"`
		} `json:"steamuser"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	return len(cfg.SteamUser.General.WallpaperConfig.SelectedWallpapers) == 0
}

// scheduleReapply debounces re-writing our wallpaper state so we don't
// fight WE in a tight loop if it writes multiple times in quick succession.
func (w *Watcher) scheduleReapply() {
	w.mu.Lock()
	defer w.mu.Unlock()
	wepath := w.wepath // capture under lock; Rebind may change it before the timer fires
	if w.reapply != nil {
		w.reapply.Stop()
	}
	w.reapply = time.AfterFunc(300*time.Millisecond, func() {
		if err := writeWESelectedWallpapers(wepath, w.ws.state.Monitors, w.ws.monitorInfos); err != nil {
			log.Printf("[wepapered] reapply error: %v", err)
		} else {
			log.Printf("[wepapered] selectedwallpapers reapplied (%d monitor(s))", len(w.ws.state.Monitors))
		}
	})
}
