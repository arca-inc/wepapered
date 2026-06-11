package main

import (
	"fmt"
	"log"
	"os/exec"

	"github.com/gorilla/websocket"
)

func (s *WSServer) handleInstall(conn *websocket.Conn, msg WEMessage) {
	if msg.Method == "subscribe" && len(msg.Args) > 0 {
		if id, ok := msg.Args[0].(string); ok {
			log.Printf("[WE] subscribe called for %s - opening in Steam", id)
			exec.Command("xdg-open", "steam://url/CommunityFilePage/"+id).Start()
		} else if idFloat, ok := msg.Args[0].(float64); ok {
			idStr := fmt.Sprintf("%d", int(idFloat))
			log.Printf("[WE] subscribe called for %s - opening in Steam", idStr)
			exec.Command("xdg-open", "steam://url/CommunityFilePage/"+idStr).Start()
		}
	}
}

// handleUI handles methods the WE UI invokes on the native `ui` object.
func (s *WSServer) handleUI(conn *websocket.Conn, msg WEMessage) {
	switch msg.Method {
	case "subscribeWorkshopItem":
		// The browse grid's Download button subscribes via the Steam client, which
		// we can't drive without its API. Open each item's Steam Workshop page so
		// the user can subscribe there (their Steam is running).
		ids := collectWorkshopIDs(msg.Args)
		for _, id := range ids {
			log.Printf("[WE] subscribeWorkshopItem %s — opening Steam Workshop page", id)
			exec.Command("xdg-open", "steam://url/CommunityFilePage/"+id).Start()
		}
		// Resolve the caller's promise (callDeferred waits on <method>Callback).
		s.sendCallback(conn, msg.Method+"Callback", nil)
	default:
		log.Printf("[WE] ui.%s", msg.Method)
	}
}

// collectWorkshopIDs flattens the argument(s) of a ui subscribe call into a list
// of workshop id strings, accepting a single id, a number, or an array of either.
func collectWorkshopIDs(args []interface{}) []string {
	var out []string
	add := func(v interface{}) {
		switch t := v.(type) {
		case string:
			if t != "" {
				out = append(out, t)
			}
		case float64:
			out = append(out, fmt.Sprintf("%d", int(t)))
		}
	}
	for _, a := range args {
		if arr, ok := a.([]interface{}); ok {
			for _, e := range arr {
				add(e)
			}
		} else {
			add(a)
		}
	}
	return out
}
