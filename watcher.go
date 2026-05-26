package main

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
	wepath  string
	fsw     *fsnotify.Watcher
	done    chan struct{}
	ws      *WSServer
	mu      sync.Mutex
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

	target := filepath.Join(w.wepath, "config.json")
	if err := fsw.Add(target); err != nil {
		fsw.Close()
		return fmt.Errorf("cannot watch %s: %w", target, err)
	}
	log.Printf("[wepapered] watching: %s", target)

	go func() {
		for {
			select {
			case event, ok := <-fsw.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					w.handleChange(target)
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
			w.scheduleReapply(path)
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
func (w *Watcher) scheduleReapply(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.reapply != nil {
		w.reapply.Stop()
	}
	w.reapply = time.AfterFunc(300*time.Millisecond, func() {
		if err := writeWESelectedWallpapers(w.wepath, w.ws.state.Monitors, w.ws.monitorInfos); err != nil {
			log.Printf("[wepapered] reapply error: %v", err)
		} else {
			log.Printf("[wepapered] selectedwallpapers reapplied (%d monitor(s))", len(w.ws.state.Monitors))
		}
	})
}
