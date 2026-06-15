package daemon

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"fyne.io/systray"

	"wepapered/internal/compositor"
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

	// Detect the windowing system. With no supported compositor (e.g. started
	// outside a Wayland session) there's nothing to render on — exit
	// cleanly (status 0) so a systemd Restart=on-failure doesn't loop.
	comp, err := compositor.Detect()
	if err != nil {
		log.Printf("[wepapered] %v — not starting.", err)
		return
	}
	sys = comp

	log.Printf("[wepapered] starting (version %s, compositor %s)", buildVersion, sys.Name())

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

	// Acquire the control socket FIRST as a single-instance gate. If another daemon
	// already owns it, exit now — before touching renderers — so we never end up with
	// two daemons fighting over the same outputs (which crash-loops LWE).
	ctrlLn, err := acquireControlSocket()
	if err != nil {
		log.Fatalf("[wepapered] not starting (%v); control socket %s", err, controlSocketPath())
	}

	// Serve the browse UI / WebSocket on a random loopback port; clients discover it
	// over the control socket (no fixed, guessable port).
	httpLn, port, err := listenRandomPort()
	if err != nil {
		ctrlLn.Close()
		log.Fatalf("[wepapered] %v", err)
	}

	ws := newWSServer(cfg)
	ws.port = port
	ws.Serve(httpLn)
	ws.ServeControl(ctrlLn)
	log.Printf("[wepapered] control socket %s → UI port %d", controlSocketPath(), port)

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

	// Single graceful-shutdown path, shared by the tray Quit, SIGTERM, and the
	// control-socket STOP (wepaperedctl stop). sync.Once so it's safe to call from
	// more than one of those. renderer.Stop() kills the LWE subprocesses; the extra
	// killStrayRenderers() reaps any that escaped.
	var shutdownOnce sync.Once
	cleanup := func() {
		shutdownOnce.Do(func() {
			log.Println("[wepapered] stopping")
			ws.playlists.Stop()
			w.Stop()
			ws.renderer.Stop()
			killStrayRenderers()
			ws.discord.Close()
			ctrlLn.Close() // unlinks the control socket file
		})
	}

	tray := newTrayManager(cfg, port, cleanup)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		log.Println("[wepapered] signal received, stopping")
		systray.Quit() // → tray onExit → cleanup → exit
	}()

	// tray.Run() blocks the main thread; onExit runs cleanup. The fallback call
	// covers the rare case Run() returns without onExit firing.
	tray.Run()
	cleanup()
}
