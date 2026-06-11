package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

var (
	authorCache = make(map[string]string) // workshop ID -> author name
	authorMu    sync.Mutex
)

// fetchAuthors takes a list of workshop IDs, queries the Steam API,
// and returns a map of workshopID -> author name.
func fetchAuthors(workshopIDs []string) map[string]string {
	result := make(map[string]string)
	if len(workshopIDs) == 0 {
		return result
	}

	data := url.Values{}
	data.Set("itemcount", fmt.Sprintf("%d", len(workshopIDs)))
	for i, id := range workshopIDs {
		data.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}

	resp, err := http.Post("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiRes struct {
		Response struct {
			PublishedFileDetails []struct {
				PublishedFileID string `json:"publishedfileid"`
				Creator         string `json:"creator"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	json.Unmarshal(body, &apiRes)

	creatorToNames := make(map[string]string)
	for _, item := range apiRes.Response.PublishedFileDetails {
		if item.Creator != "" && item.Creator != "0" {
			if name, ok := creatorToNames[item.Creator]; ok {
				result[item.PublishedFileID] = name
				continue
			}
			name := fetchCreatorName(item.Creator)
			if name != "" {
				creatorToNames[item.Creator] = name
				result[item.PublishedFileID] = name
			}
		}
	}
	return result
}

func fetchCreatorName(creatorID string) string {
	resp, err := http.Get("https://steamcommunity.com/profiles/" + creatorID + "/?xml=1")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	
	var profile struct {
		SteamID string `xml:"steamID"`
	}
	if err := xml.Unmarshal(body, &profile); err != nil {
		return ""
	}
	return profile.SteamID
}

func (s *WSServer) enrichLibraryWithAuthors(conn interface{}) {
	// Not implemented directly yet, just a helper file
}
