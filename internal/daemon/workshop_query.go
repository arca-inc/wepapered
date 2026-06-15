package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// toStringSlice coerces a JSON array (from the bridge) into a []string, dropping
// non-string and empty entries.
func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// isWorkshopInstalled reports whether a subscribed workshop item is present on
// disk (…/steamapps/workshop/content/431960/<id>/).
func isWorkshopInstalled(wePath, id string) bool {
	if wePath == "" || id == "" {
		return false
	}
	steamapps := filepath.Dir(filepath.Dir(wePath))
	_, err := os.Stat(filepath.Join(steamapps, "workshop", "content", "431960", id))
	return err == nil
}

// handleQueryWorkshop debounces incoming Workshop queries. The browse UI fires
// queryWorkshop in bursts (tab switch, filter init, and — fatally — on every
// rejected response) and resolves them all through the single shared
// window.queryWorkshopCallback slot. Because our Steam HTTP round-trip is slow
// (~0.8s) compared with the native client, multiple queries are in flight at
// once: by the time an early response arrives, the slot points at a later
// request, the token no longer matches, and the UI re-queries — an endless
// loop ("Waiting for a response from Steam…"). Coalescing a burst into a single
// query for the *latest* request means the response token matches the slot the
// UI is actually waiting on, so it's accepted and the loop never starts.
func (s *WSServer) handleQueryWorkshop(conn *websocket.Conn, msg WEMessage) {
	s.qwMu.Lock()
	s.qwConn = conn
	s.qwMsg = msg
	if s.qwTimer != nil {
		s.qwTimer.Stop()
	}
	s.qwTimer = time.AfterFunc(250*time.Millisecond, func() {
		s.qwMu.Lock()
		c, m := s.qwConn, s.qwMsg
		s.qwMu.Unlock()
		s.doQueryWorkshop(c, m)
	})
	s.qwMu.Unlock()
}

func (s *WSServer) doQueryWorkshop(conn *websocket.Conn, msg WEMessage) {
	if s.cfg.Load().SteamAPIKey == "" {
		log.Println("[WE] queryWorkshop: missing Steam API key")
		// Clear the grid, then tell the UI to instruct the user to add a key
		// (Workshop/Explore can't be queried without one).
		s.sendCallback(conn, msg.Callback, map[string]interface{}{"wallpapers": []UIWallpaper{}, "total": 0})
		s.Broadcast(map[string]interface{}{"type": "apikeymissing"})
		return
	}

	if len(msg.Args) == 0 {
		s.sendCallback(conn, msg.Callback, map[string]interface{}{"wallpapers": []UIWallpaper{}, "total": 0})
		return
	}
	opts, ok := msg.Args[0].(map[string]interface{})
	if !ok {
		s.sendCallback(conn, msg.Callback, map[string]interface{}{"wallpapers": []UIWallpaper{}, "total": 0})
		return
	}

	page := 1
	if p, ok := opts["page"].(float64); ok {
		page = int(p)
	}
	numPerPage := 30
	if n, ok := opts["numperpage"].(float64); ok {
		numPerPage = int(n)
	}
	// The browse UI sends the search string as "text" (legacy spies used
	// "searchtext"); accept either.
	searchText := ""
	if st, ok := opts["text"].(string); ok {
		searchText = st
	} else if st, ok := opts["searchtext"].(string); ok {
		searchText = st
	}

	// The UI sends a sort string (sort: "top_rated"/"trend"/…), not a numeric
	// query_type. Map it to Steam's IPublishedFileService query_type enum.
	// 0=RankedByVote, 1=RankedByPublicationDate, 3=RankedByTrend, 21=LastUpdated.
	qt := 3
	switch s, _ := opts["sort"].(string); s {
	case "top_rated", "most_popular":
		qt = 0
	case "trend", "trending":
		qt = 3
	case "newest", "recent", "most_recent":
		qt = 1
	case "updated", "last_updated":
		qt = 21
	}

	data := url.Values{}
	data.Set("key", s.cfg.Load().SteamAPIKey)
	data.Set("appid", "431960")
	data.Set("page", fmt.Sprint(page))
	data.Set("numperpage", fmt.Sprint(numPerPage))
	data.Set("search_text", searchText)
	data.Set("query_type", fmt.Sprint(qt))
	data.Set("return_tags", "1")
	data.Set("return_details", "1")
	data.Set("return_metadata", "1")

	// Forward the UI's tag filters to Steam. The browse panel encodes every
	// filter (genre, type, category, resolution and the content rating, i.e. the
	// Everyone/Questionable/Mature "-18" toggle) as required/excluded tags; an
	// unchecked rating ends up in excludedTags, so simply relaying them makes the
	// whole filter panel — including showing mature content — work.
	reqTags := toStringSlice(opts["requiredTags"])
	excTags := toStringSlice(opts["excludedTags"])
	for i, t := range reqTags {
		data.Set(fmt.Sprintf("requiredtags[%d]", i), t)
	}
	for i, t := range excTags {
		data.Set(fmt.Sprintf("excludedtags[%d]", i), t)
	}
	if len(reqTags) > 0 {
		data.Set("match_all_tags", "1")
	}

	resp, err := http.Get("https://api.steampowered.com/IPublishedFileService/QueryFiles/v1/?" + data.Encode())
	if err != nil {
		log.Printf("[WE] queryWorkshop error: %v", err)
		s.sendCallback(conn, msg.Callback, map[string]interface{}{"wallpapers": []UIWallpaper{}, "total": 0})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiRes struct {
		Response struct {
			PublishedFileDetails []struct {
				PublishedFileID string `json:"publishedfileid"`
				Creator         string `json:"creator"`
				Title           string `json:"title"`
				PreviewURL      string `json:"preview_url"`
				Tags            []struct{ Tag string `json:"tag"` } `json:"tags"`
				Visibility      int    `json:"visibility"`
				Banned          int    `json:"banned"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	json.Unmarshal(body, &apiRes)

	results := make([]UIWallpaper, 0)
	for _, item := range apiRes.Response.PublishedFileDetails {
		if item.Visibility != 0 || item.Banned != 0 {
			continue // Only show public, unbanned items
		}
		
		var tags []string
		var contentType = "scene"
		for _, t := range item.Tags {
			tags = append(tags, t.Tag)
			if strings.ToLower(t.Tag) == "video" {
				contentType = "video"
			} else if strings.ToLower(t.Tag) == "web" {
				contentType = "web"
			}
		}

		// The wallpaper tile only renders an action for status "installed" or
		// "downloadable" (any other value, like the old "notinstalled", shows no
		// subscribe/download button so the item looks already owned). A workshop
		// item we don't have locally is "downloadable"; one present on disk is
		// "installed".
		status := "downloadable"
		if isWorkshopInstalled(s.cfg.Load().WEPath, item.PublishedFileID) {
			status = "installed"
		}

		w := UIWallpaper{
			File:         item.PublishedFileID,
			Title:        item.Title,
			Type:         contentType,
			Preview:      item.PreviewURL,
			PreviewSmall: item.PreviewURL,
			WorkshopID:   item.PublishedFileID,
			WorkshopURL:  "https://steamcommunity.com/sharedfiles/filedetails/?id=" + item.PublishedFileID,
			ItemID:       item.PublishedFileID,
			Tags:         strings.Join(tags, ","),
			Status:       status,
			Local:        false,
			Approved:     false,
		}
		
		if item.Creator != "" && item.Creator != "0" {
			// Fast check if we already have the name
			authorMu.Lock()
			if name, ok := authorCache[item.Creator]; ok {
				w.Author = name
			} else {
				w.Author = item.Creator // fallback until resolved
			}
			authorMu.Unlock()
		}

		results = append(results, w)
	}
	s.markFavorites(results)

	// The browse controller's success handler (r in scripts.js) rejects the
	// result unless e.token === i.token, falling back to an endless re-query
	// (the "Waiting for a response from Steam…" spinner). Echo the request token
	// and supply pagecount, which it feeds to updatePagination.
	pageCount := page + 1 // assume there's a next page…
	if len(results) < numPerPage {
		pageCount = page // …unless this page came back short (the last one)
	}
	resMap := map[string]interface{}{
		"wallpapers": results,
		"token":      opts["token"],
		"page":       page,
		"pagecount":  pageCount,
		"total":      len(results),
		"isbackup":   false,
	}

	s.sendCallback(conn, msg.Callback, resMap)
}

func (s *WSServer) sendCallback(conn *websocket.Conn, cb string, args interface{}) {
	if cb == "" {
		return
	}
	reply, _ := json.Marshal(map[string]interface{}{
		"callback": cb,
		"args": []interface{}{ args },
	})
	s.mu.Lock()
	conn.WriteMessage(websocket.TextMessage, reply)
	s.mu.Unlock()
}
