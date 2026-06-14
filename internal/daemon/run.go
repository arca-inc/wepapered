package daemon

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/systray"
)

// Run is the wepapered daemon entry point. It wires up WSServer → Renderer →
// Watcher, applies saved state on startup, serves the browse UI, runs the tray,
// and blocks until the tray quits or a termination signal arrives.
func Run() {
	dumpLib := flag.Bool("dump-library", false, "print the enumerated wallpaper library as JSON and exit")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Printf("config load error: %v, using defaults", err)
		cfg = &Config{}
	}

	if *dumpLib {
		lib := enumerateLibrary(resolveWEPath(cfg), cfg.CustomDirs)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(lib) //nolint
		return
	}

	// Resolve (and repair) the WE path: auto-detect if the configured one is
	// empty or no longer points at a real install (e.g. a stale Flatpak path
	// after switching to a native Steam, or a fresh install).
	if !weDirValid(cfg.WEPath) {
		resolved := resolveWEPath(cfg)
		if resolved == "" {
			log.Fatal("Wallpaper Engine path not found. Run `wepapered-settings` to configure.")
		}
		if resolved != cfg.WEPath {
			log.Printf("WE path %q invalid; auto-detected %q", cfg.WEPath, resolved)
		}
		cfg.WEPath = resolved
		if err := saveConfig(cfg); err != nil {
			log.Printf("could not save config: %v", err)
		}
	}

	// Bind the control port FIRST as a single-instance gate. If another daemon
	// already owns it, exit now — before touching renderers — so we never end up
	// with two daemons fighting over the same outputs (which crash-loops LWE).
	ws := newWSServer(cfg)
	if err := ws.Start(controlAddr); err != nil {
		log.Fatalf("[wepapered] a daemon is already running (%s in use); not starting a second. (%v)", controlAddr, err)
	}

	// Clean up any renderers orphaned by a previous instance that didn't shut
	// down cleanly, so we don't end up with duplicate processes per output.
	killStrayRenderers()

	// Pre-initialise the loading overlay (its own GTK thread) so it pops instantly.
	startLoadingOverlay()

	// Discord Rich Presence (optional; connects in the background when Discord runs).
	go ws.discord.Run()
	ws.updateDiscordPresence()

	// Resume per-monitor playlists (arms rotation timers and mirrors each
	// playlist's current item into state) before the initial render.
	ws.playlists.StartAll()

	// Apply saved wallpapers immediately on startup.
	ws.stateMu.Lock()
	startSnap := ws.state.snapshot()
	startN := len(ws.state.Monitors)
	ws.stateMu.Unlock()
	if startN > 0 {
		go ws.renderer.Apply(startSnap)
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
	ws.watcher = w // let /reload re-point the watch when the WE path changes
	if err := w.Start(); err != nil {
		// Non-fatal: the watcher only re-asserts our selection when WE clears it.
		// The daemon renders fine without it, so log and carry on.
		log.Printf("watcher start failed (continuing without WE config re-assert): %v", err)
	}

	tray := newTrayManager(cfg)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		log.Println("[wepapered] signal received, stopping")
		systray.Quit()
	}()

	// tray.Run() blocks the main thread.
	tray.Run()

	log.Println("[wepapered] stopping")
	ws.playlists.Stop()
	w.Stop()
	ws.renderer.Stop()
	ws.discord.Close()
}
