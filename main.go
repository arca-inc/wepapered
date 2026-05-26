package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ui := flag.Bool("ui", false, "open config window")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Printf("config load error: %v, using defaults", err)
		cfg = &Config{}
	}

	if *ui {
		runConfigUI(cfg)
		return
	}

	// Daemon mode
	if cfg.WEPath == "" {
		cfg.WEPath = autoDetectWEPath()
		if cfg.WEPath == "" {
			log.Fatal("Wallpaper Engine path not found. Run `wepapered --ui` to configure.")
		}
		if err := saveConfig(cfg); err != nil {
			log.Printf("could not save config: %v", err)
		}
		log.Printf("auto-detected WE path: %s", cfg.WEPath)
	}

	ws := newWSServer(cfg)
	ws.Start("127.0.0.1:9001")

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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[wepapered] stopping")
	w.Stop()
	ws.renderer.Stop()
}
