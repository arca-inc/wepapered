//go:build ignore

// Standalone WebSocket probe for the daemon's /we bridge. Excluded from the
// package build (it has its own main); run with: go run test_ws.go
package main

import (
	"log"
	"strings"
	"github.com/gorilla/websocket"
)

func main() {
	c, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:9001/we", nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	err = c.WriteJSON(map[string]interface{}{
		"object":   "browseWallpaperObject",
		"method":   "queryWorkshop",
		"callback": "test_callback",
		"args": []interface{}{
			map[string]interface{}{
				"querytype": 1,
				"page": 1,
				"numperpage": 5,
				"searchtext": "",
			},
		},
	})
	if err != nil {
		log.Fatal("write:", err)
	}

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Fatal("read:", err)
		}
		if strings.Contains(string(message), "test_callback") {
			log.Printf("SUCCESS recv callback: %s", string(message)[:300])
			break
		}
	}
}
