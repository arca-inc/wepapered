package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type CEFPage struct {
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerUrl string `json:"webSocketDebuggerUrl"`
}

func TryInjectCEF() {
	for {
		time.Sleep(5 * time.Second)
		resp, err := http.Get("http://127.0.0.1:9222/json")
		if err != nil {
			continue
		}
		
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		
		var pages []CEFPage
		if err := json.Unmarshal(body, &pages); err != nil {
			continue
		}
		
		for _, page := range pages {
			if strings.Contains(page.URL, "index.html") && page.WebSocketDebuggerUrl != "" {
				// Inject script
				injectScript(page.WebSocketDebuggerUrl)
				break
			}
		}
	}
}

func injectScript(wsURL string) {
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Printf("[injector] Failed to connect to CEF debugger: %v", err)
		return
	}
	defer conn.Close()

	// Read shim script
	shimPath := filepath.Join("uihost", "wepapered-ui-shim.js")
	shim, err := os.ReadFile(shimPath)
	if err != nil {
		log.Printf("[injector] Failed to read shim: %v", err)
		return
	}

	cmd := map[string]interface{}{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]interface{}{
			"expression": string(shim),
		},
	}

	if err := conn.WriteJSON(cmd); err != nil {
		log.Printf("[injector] Failed to send injection command: %v", err)
		return
	}
	log.Printf("[injector] Successfully injected wepapered-ui-shim.js into the native Steam UI!")
	
	// Wait to avoid immediate reconnects if we keep checking
	time.Sleep(10 * time.Second)
}
