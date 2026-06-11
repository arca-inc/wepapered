package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UIWallpaper is one entry as the hosted Wallpaper Engine browse UI expects it
// (pushed via browseWallpapersCtrl.updateWallpapers). The field shape was
// validated against the real UI in tools/uiprobe — see the bridge contract.
type UIWallpaper struct {
	File          string   `json:"file"`         // identity key; the daemon resolves it on selectWallpaper
	Title         string   `json:"title"`
	Type          string   `json:"type"`         // "scene" | "video" | "web" | "image" | …
	Preview       string   `json:"preview"`      // ready-to-use url (file://…); UI wraps it in url(...)
	PreviewSmall  string   `json:"previewsmall"`
	WorkshopID    string   `json:"workshopid,omitempty"`
	ItemID        string   `json:"itemid,omitempty"`
	ContentRating string   `json:"contentrating"`
	Tags          []string `json:"tags"`
	Status        string   `json:"status"` // "installed"
	Local         bool     `json:"local"`  // true for myprojects, false for workshop items
	Approved      bool     `json:"approved"`
}

// enumerateLibrary scans the installed Wallpaper Engine content and returns the
// browse list: subscribed Steam Workshop items (…/431960/<id>/) plus local
// myprojects. Pure Go, no Steam — this is the "browse-only" library.
func enumerateLibrary(wePath string) []UIWallpaper {
	var out []UIWallpaper

	steamapps := filepath.Dir(filepath.Dir(wePath)) // …/steamapps
	workshopRoot := filepath.Join(steamapps, "workshop", "content", "431960")
	out = append(out, scanWallpaperDir(workshopRoot, false)...)

	myProjects := filepath.Join(wePath, "projects", "myprojects")
	out = append(out, scanWallpaperDir(myProjects, true)...)

	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// loadLocale merges WE's core_ and ui_ translation files for a language into a
// flat key→string table (skipping any non-string/nested entries).
func loadLocale(wePath, lang string) map[string]string {
	table := make(map[string]string)
	for _, prefix := range []string{"core", "ui"} {
		langLower := strings.ToLower(lang)
		path := filepath.Join(wePath, "locale", prefix+"_"+langLower+".json")
		if _, err := os.Stat(path); err != nil {
			if matches, _ := filepath.Glob(filepath.Join(wePath, "locale", prefix+"_"+langLower+"*.json")); len(matches) > 0 {
				path = matches[0]
			} else if matches, _ := filepath.Glob(filepath.Join(wePath, "locale", prefix+"_"+strings.Split(langLower, "-")[0]+"*.json")); len(matches) > 0 {
				path = matches[0]
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		for k, v := range raw {
			if s, ok := v.(string); ok {
				table[k] = s
			}
		}
	}
	return table
}

func scanWallpaperDir(root string, local bool) []UIWallpaper {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []UIWallpaper
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		meta := readProjectMeta(dir)
		if meta == nil {
			continue
		}
		if w, ok := uiWallpaperFromMeta(dir, e.Name(), meta, local); ok {
			out = append(out, w)
		}
	}
	return out
}

func uiWallpaperFromMeta(dir, dirName string, meta *ProjectJSON, local bool) (UIWallpaper, bool) {
	previewFile := meta.Preview
	if previewFile == "" {
		previewFile = "preview.jpg"
	}
	preview := "file://" + filepath.Join(dir, previewFile)
	// Make it an HTTP URL if it's within steamapps
	if idx := strings.Index(dir, "/steamapps/"); idx >= 0 {
		preview = "/steamapps/" + filepath.Join(dir[idx+len("/steamapps/"):], previewFile)
	}

	workshopID := meta.WorkshopID
	if workshopID == "" && !local {
		workshopID = dirName // workshop content dirs are named by their ID
	}

	title := meta.Title
	if title == "" {
		title = dirName
	}

	contentRating := meta.ContentRating
	if contentRating == "" {
		contentRating = "Everyone"
	}

	tags := meta.Tags
	if tags == nil {
		tags = []string{}
	}

	return UIWallpaper{
		File:          dir,
		Title:         title,
		Type:          meta.Type,
		Preview:       preview,
		PreviewSmall:  preview,
		WorkshopID:    workshopID,
		ItemID:        workshopID,
		ContentRating: contentRating,
		Tags:          tags,
		Status:        "installed",
		Local:         local,
		Approved:      true,
	}, true
}
