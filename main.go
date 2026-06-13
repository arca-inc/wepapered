package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/systray"
)

func main() {
	config := flag.Bool("config", false, "open the configuration window")
	ui := flag.Bool("ui", false, "alias for --config (deprecated)")
	gui := flag.Bool("gui", false, "open the native WE browse window (starts the daemon if needed)")
	dumpLib := flag.Bool("dump-library", false, "print the enumerated wallpaper library as JSON and exit")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Printf("config load error: %v, using defaults", err)
		cfg = &Config{}
	}

	if *config || *ui {
		runConfigUI(cfg)
		return
	}

	if *gui {
		runGUI(cfg)
		return
	}

	if *dumpLib {
		lib := enumerateLibrary(resolveWEPath(cfg), cfg.CustomDirs)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(lib) //nolint
		return
	}

	// Daemon mode — resolve (and repair) the WE path: auto-detect if the
	// configured one is empty or no longer points at a real install (e.g. a
	// stale Flatpak path after switching to a native Steam, or a fresh install).
	if !weDirValid(cfg.WEPath) {
		resolved := resolveWEPath(cfg)
		if resolved == "" {
			log.Fatal("Wallpaper Engine path not found. Run `wepapered --ui` to configure.")
		}
		if resolved != cfg.WEPath {
			log.Printf("WE path %q invalid; auto-detected %q", cfg.WEPath, resolved)
		}
		cfg.WEPath = resolved
		if err := saveConfig(cfg); err != nil {
			log.Printf("could not save config: %v", err)
		}
	}

	// Clean up any renderers orphaned by a previous instance that didn't shut
	// down cleanly, so we don't end up with duplicate processes per output.
	killStrayRenderers()

	ws := newWSServer(cfg)
	ws.Start("127.0.0.1:9001")

	// Discord Rich Presence (optional; connects in the background when Discord runs).
	go ws.discord.Run()
	ws.updateDiscordPresence()

	// Apply saved wallpapers immediately on startup.
	if len(ws.state.Monitors) > 0 {
		go ws.renderer.Apply(ws.state)
	}

	// Watchdog: re-apply when a screen process dies unexpectedly.
	go func() {
		for range ws.renderer.applyTrigger {
			ws.renderer.mu.Lock()
			state := ws.renderer.lastState
			ws.renderer.mu.Unlock()
			if state != nil {
				log.Printf("[renderer] watchdog: screen died, re-applying state")
				ws.renderer.Apply(state)
			}
		}
	}()

	w := newWatcher(cfg, ws)
	if err := w.Start(); err != nil {
		log.Fatalf("watcher start failed: %v", err)
	}

	tray := newTrayManager(cfg)
	
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-sig
		log.Println("[wepapered] signal received, stopping")
		systray.Quit()
	}()

	// tray.Run() blocks the main thread
	tray.Run()

	log.Println("[wepapered] stopping")
	w.Stop()
	ws.renderer.Stop()
	ws.discord.Close()
}
