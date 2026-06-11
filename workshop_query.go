package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

func (s *WSServer) handleQueryWorkshop(conn *websocket.Conn, msg WEMessage) {
	if s.cfg.SteamAPIKey == "" {
		log.Println("[WE] queryWorkshop: missing Steam API key")
		s.sendCallback(conn, msg.Callback, []UIWallpaper{})
		return
	}

	if len(msg.Args) == 0 {
		s.sendCallback(conn, msg.Callback, []UIWallpaper{})
		return
	}
	opts, ok := msg.Args[0].(map[string]interface{})
	if !ok {
		s.sendCallback(conn, msg.Callback, []UIWallpaper{})
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
	searchText := ""
	if st, ok := opts["searchtext"].(string); ok {
		searchText = st
	}
	
	// Query_type mapping: 1=Trend, 2=Recent
	qt := 1
	if q, ok := opts["querytype"].(float64); ok {
		qt = int(q)
	}

	data := url.Values{}
	data.Set("key", s.cfg.SteamAPIKey)
	data.Set("appid", "431960")
	data.Set("page", fmt.Sprint(page))
	data.Set("numperpage", fmt.Sprint(numPerPage))
	data.Set("search_text", searchText)
	data.Set("query_type", fmt.Sprint(qt))
	data.Set("return_tags", "1")
	data.Set("return_details", "1")
	data.Set("return_metadata", "1")

	resp, err := http.Get("https://api.steampowered.com/IPublishedFileService/QueryFiles/v1/?" + data.Encode())
	if err != nil {
		log.Printf("[WE] queryWorkshop error: %v", err)
		s.sendCallback(conn, msg.Callback, []UIWallpaper{})
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

	var results []UIWallpaper
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

		w := UIWallpaper{
			File:         item.PublishedFileID,
			Title:        item.Title,
			Type:         contentType,
			Preview:      item.PreviewURL,
			PreviewSmall: item.PreviewURL,
			WorkshopID:   item.PublishedFileID,
			ItemID:       item.PublishedFileID,
			Tags:         tags,
			Status:       "notinstalled",
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

	s.sendCallback(conn, msg.Callback, results)
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
