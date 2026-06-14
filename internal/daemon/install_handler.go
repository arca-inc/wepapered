package daemon

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
		ok := steamSubscribeDownload(ids,
			func(id string, down, total uint64) {
				pct := 0.0
				label := ""
				if total > 0 {
					pct = float64(down) * 100 / float64(total)
					label = fmt.Sprintf("%.0f%%", pct)
				}
				log.Printf("[steam-ugc] progress %s %.0f%% (%d/%d)", id, pct, down, total)
				s.Broadcast(map[string]interface{}{
					"type": "wsprogress", "workshopid": id,
					"status": "downloading", "percent": pct, "label": label,
				})
			},
			func(id string) {
				notifyUser(fmt.Sprintf("Wallpaper downloaded (%s)", id))
				s.Broadcast(map[string]interface{}{
					"type": "wsprogress", "workshopid": id,
					"status": "installed", "percent": 100.0, "label": "",
				})
			},
			func(reason string) {
				log.Printf("[steam-ugc] download failed: %s", reason)
				notifyUser(reason)
				// Tell the UI to show the error and clear the stuck download rings.
				s.Broadcast(map[string]interface{}{"type": "wserror", "message": reason})
				for _, id := range ids {
					s.Broadcast(map[string]interface{}{
						"type": "wsprogress", "workshopid": id,
						"status": "downloadable", "percent": 0.0, "label": "",
					})
				}
			})
		if !ok {
			for _, id := range ids {
				log.Printf("[WE] steam-ugc unavailable — opening Steam Workshop page for %s", id)
				exec.Command("xdg-open", "steam://url/CommunityFilePage/"+id).Start()
			}
		}
		// Resolve the caller's promise (callDeferred waits on <method>Callback).
		s.sendCallback(conn, msg.Method+"Callback", nil)

	case "unsubscribeWorkshopItem":
		ids := collectWorkshopIDs(msg.Args)
		log.Printf("[WE] unsubscribeWorkshopItem %v", ids)
		steamUnsubscribe(ids, func(id string) {
			s.Broadcast(map[string]interface{}{
				"type": "wsprogress", "workshopid": id,
				"status": "downloadable", "percent": 0.0, "label": "",
			})
		})
		s.sendCallback(conn, msg.Method+"Callback", nil)

	case "shellexecute", "openplatformbrowser", "openhtmlexternally":
		// WE opens external URLs/files through these host hooks (e.g. the right-click
		// "Open in workshop" → openUrl(workshopurl) → ui.shellexecute, plus help and
		// agreement links). On Linux, xdg-open handles http(s)://, steam:// and paths.
		if len(msg.Args) > 0 {
			if target, ok := msg.Args[0].(string); ok && target != "" {
				log.Printf("[WE] ui.%s → xdg-open %s", msg.Method, target)
				exec.Command("xdg-open", target).Start() //nolint
			}
		}

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
