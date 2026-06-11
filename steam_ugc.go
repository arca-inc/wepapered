package main

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// steamUGCBin is the helper that subscribes to / downloads workshop items through
// the running Steam client (see tools/steam-ugc/steam_ugc.c). It lives next to
// the LWE binaries.
var steamUGCBin = filepath.Join(lweOutputDir, "wepapered-steam-ugc")

// steamSubscribeDownload asks the Steam client to subscribe to and download the
// given workshop ids via ISteamUGC. It runs the helper asynchronously and calls
// onInstalled for each id Steam reports as installed (the content lands in
// …/workshop/content/431960/<id>/, which enumerateLibrary already scans).
// Returns false if the helper isn't available, so the caller can fall back to
// opening the Steam Workshop page.
func steamSubscribeDownload(ids []string, onInstalled func(id string)) bool {
	if len(ids) == 0 {
		return true
	}
	if _, err := os.Stat(steamUGCBin); err != nil {
		return false
	}
	cmd := exec.Command(steamUGCBin, ids...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[steam-ugc] start failed: %v", err)
		return false
	}
	log.Printf("[steam-ugc] subscribing/downloading %v", ids)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if id := strings.TrimPrefix(line, "installed "); id != line {
				log.Printf("[steam-ugc] installed %s", id)
				if onInstalled != nil {
					onInstalled(id)
				}
			}
		}
		if err := cmd.Wait(); err != nil {
			log.Printf("[steam-ugc] helper exited: %v", err)
		}
	}()
	return true
}
