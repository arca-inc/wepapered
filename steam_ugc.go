package main

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// steamUGCBin is the helper that subscribes to / downloads workshop items through
// the running Steam client (see tools/steam-ugc/steam_ugc.c). It lives next to
// the LWE binaries.
var steamUGCBin = filepath.Join(lweOutputDir, "wepapered-steam-ugc")

// steamSubscribeDownload asks the Steam client to subscribe to and download the
// given workshop ids via ISteamUGC. It runs the helper asynchronously, calling
// onProgress as bytes flow and onInstalled when Steam reports an id installed
// (the content lands in …/workshop/content/431960/<id>/, which enumerateLibrary
// already scans). Returns false if the helper isn't available, so the caller can
// fall back to opening the Steam Workshop page.
func steamSubscribeDownload(ids []string, onProgress func(id string, down, total uint64), onInstalled func(id string)) bool {
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
			f := strings.Fields(strings.TrimSpace(sc.Text()))
			switch {
			case len(f) == 2 && f[0] == "installed":
				log.Printf("[steam-ugc] installed %s", f[1])
				if onInstalled != nil {
					onInstalled(f[1])
				}
			case len(f) == 4 && f[0] == "progress":
				if onProgress != nil {
					down, _ := strconv.ParseUint(f[2], 10, 64)
					total, _ := strconv.ParseUint(f[3], 10, 64)
					onProgress(f[1], down, total)
				}
			}
		}
		if err := cmd.Wait(); err != nil {
			log.Printf("[steam-ugc] helper exited: %v", err)
		}
	}()
	return true
}

// steamUnsubscribe asks the Steam client to unsubscribe from the given workshop
// ids (ISteamUGC::UnsubscribeItem); Steam removes the local files. Runs the
// helper asynchronously, calling onDone for each id. Returns false if the helper
// isn't available.
func steamUnsubscribe(ids []string, onDone func(id string)) bool {
	if len(ids) == 0 {
		return true
	}
	if _, err := os.Stat(steamUGCBin); err != nil {
		return false
	}
	args := append([]string{"--unsubscribe"}, ids...)
	cmd := exec.Command(steamUGCBin, args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[steam-ugc] unsubscribe start failed: %v", err)
		return false
	}
	log.Printf("[steam-ugc] unsubscribing %v", ids)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			f := strings.Fields(strings.TrimSpace(sc.Text()))
			if len(f) == 2 && f[0] == "unsubscribed" {
				log.Printf("[steam-ugc] unsubscribed %s", f[1])
				if onDone != nil {
					onDone(f[1])
				}
			}
		}
		cmd.Wait()
	}()
	return true
}
