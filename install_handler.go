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
		// The browse grid's Download button subscribes + downloads through the
		// Steam client via ISteamUGC (steamSubscribeDownload). On success the item
		// appears under …/workshop/content/431960/<id>/ and we notify the user. If
		// the helper isn't available, fall back to opening the Steam Workshop page.
		ids := collectWorkshopIDs(msg.Args)
		log.Printf("[WE] subscribeWorkshopItem %v", ids)
		ok := steamSubscribeDownload(ids, func(id string) {
			notifyUser(fmt.Sprintf("Wallpaper téléchargé (%s)", id))
		})
		if !ok {
			for _, id := range ids {
				log.Printf("[WE] steam-ugc unavailable — opening Steam Workshop page for %s", id)
				exec.Command("xdg-open", "steam://url/CommunityFilePage/"+id).Start()
			}
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
