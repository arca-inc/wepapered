package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	WorkshopURL   string   `json:"workshopurl,omitempty"` // Steam page; openSteamWorkshopPage reads this
	ItemID        string   `json:"itemid,omitempty"`
	ContentRating string   `json:"contentrating"`
	Tags          string   `json:"tags"` // comma-separated; WE's filter does t.tags.split(",")/indexOf
	Status        string                 `json:"status"` // "installed"
	Local         bool                   `json:"local"`  // true for myprojects, false for workshop items
	Approved      bool                   `json:"approved"`
	Author        string                 `json:"author"`
	General       map[string]interface{} `json:"general,omitempty"`
	Properties    map[string]interface{} `json:"properties,omitempty"`
}

// isNumericID reports whether s is a non-empty all-digit string (a Steam workshop ID).
func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// enumerateLibrary scans the installed Wallpaper Engine content and returns the
// browse list: subscribed Steam Workshop items (…/431960/<id>/) plus local
// myprojects. Pure Go, no Steam — this is the "browse-only" library.
func enumerateLibrary(wePath string, customDirs []string) []UIWallpaper {
	var out []UIWallpaper
	seen := map[string]bool{}
	add := func(ws []UIWallpaper) {
		for _, w := range ws {
			if seen[w.File] {
				continue
			}
			seen[w.File] = true
			out = append(out, w)
		}
	}

	// Subscribed Steam Workshop items. Scan every workshop dir we can find: the
	// one implied by the WE install path (correct for a genuine Steam install)
	// plus the real Steam library folders, so downloaded items are picked up
	// even when WE lives outside the Steam tree.
	for _, root := range workshopRoots(wePath) {
		add(scanWallpaperDir(root, false))
	}

	add(scanWallpaperDir(filepath.Join(wePath, "projects", "myprojects"), true))
	add(scanWallpaperDir(filepath.Join(wePath, "projects", "defaultprojects"), true))

	// User-configured directories of manually-downloaded wallpapers.
	for _, d := range customDirs {
		add(scanLibraryRoot(d, true))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

var libPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)

// workshopRoots returns candidate …/workshop/content/431960 directories: the one
// derived from the WE install path (correct for genuine Steam installs) plus any
// discovered from the Steam library folders (correct when WE is a manual copy
// outside the Steam tree but downloads still land in the Steam library).
func workshopRoots(wePath string) []string {
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		roots = append(roots, p)
	}

	add(filepath.Join(filepath.Dir(filepath.Dir(wePath)), "workshop", "content", "431960"))
	for _, lib := range steamLibraryDirs() {
		add(filepath.Join(lib, "steamapps", "workshop", "content", "431960"))
	}
	return roots
}

// steamLibraryDirs returns the Steam library root directories, parsed from
// libraryfolders.vdf, always including the default native and Flatpak installs.
func steamLibraryDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		// Canonicalize so symlinked roots (e.g. ~/.steam/steam -> ~/.local/share/
		// Steam) collapse to one entry instead of producing duplicate library rows.
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			p = rp
		}
		if seen[p] {
			return
		}
		seen[p] = true
		dirs = append(dirs, p)
	}

	bases := []string{
		filepath.Join(home, ".local", "share", "Steam"),
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
	}
	for _, b := range bases {
		add(b)
	}
	for _, b := range bases {
		data, err := os.ReadFile(filepath.Join(b, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range libPathRe.FindAllStringSubmatch(string(data), -1) {
			add(filepath.FromSlash(strings.ReplaceAll(m[1], `\\`, `/`)))
		}
	}
	return dirs
}

// scanLibraryRoot scans a user-provided directory. It accepts either a single
// wallpaper directory (one holding a project.json) or a parent directory holding
// many wallpaper subdirectories.
func scanLibraryRoot(root string, local bool) []UIWallpaper {
	if root == "" {
		return nil
	}
	if meta := readProjectMeta(root); meta != nil {
		if w, ok := uiWallpaperFromMeta(root, filepath.Base(root), meta, local); ok {
			return []UIWallpaper{w}
		}
		return nil
	}
	return scanWallpaperDir(root, local)
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
	// Serve via HTTP (file:// is blocked from an http page). Bundled wallpapers
	// live under <WEPath>/projects and use the /projects route; everything else
	// (workshop items, custom dirs, a Steam library on any disk) goes through the
	// generic /asset route, which serves an absolute path validated against the
	// known roots — correct regardless of where we_path points.
	preview := "/asset" + filepath.Join(dir, previewFile)
	if idx := strings.Index(dir, "/projects/"); idx >= 0 {
		preview = "/projects/" + filepath.Join(dir[idx+len("/projects/"):], previewFile)
	}

	workshopID := string(meta.WorkshopID)
	if workshopID == "" && !local {
		workshopID = dirName // workshop content dirs are named by their ID
	}

	// Steam workshop page, read by the UI's right-click "Open in workshop"
	// (openSteamWorkshopPage → openUrl(workshopurl)). Only for real (numeric) workshop
	// items; local/myprojects wallpapers have no workshop page.
	workshopURL := ""
	if isNumericID(workshopID) {
		workshopURL = "https://steamcommunity.com/sharedfiles/filedetails/?id=" + workshopID
	}

	title := meta.Title
	if title == "" {
		title = dirName
	}

	contentRating := meta.ContentRating
	if contentRating == "" {
		contentRating = "Everyone"
	}

	// WE's installed-tab filter matches against wallpaper.tags as a comma-separated
	// STRING (t.tags.split(",") / indexOf). It expects the type and source tokens to be
	// present alongside the genre tags, so build "<Type>,<Source>,<genres…>,Approved".
	var tagParts []string
	switch strings.ToLower(meta.Type) {
	case "scene":
		tagParts = append(tagParts, "Scene")
	case "video":
		tagParts = append(tagParts, "Video")
	case "web":
		tagParts = append(tagParts, "Web")
	case "application":
		tagParts = append(tagParts, "Application")
	}
	if local {
		tagParts = append(tagParts, "Local")
	} else {
		tagParts = append(tagParts, "Workshop")
	}
	tagParts = append(tagParts, meta.Tags...) // genre/utility tags from project.json
	tagParts = append(tagParts, "Approved")   // matches Approved:true below
	tags := strings.Join(tagParts, ",")

	return UIWallpaper{
		File:          dir,
		Title:         title,
		Type:          meta.Type,
		Preview:       preview,
		PreviewSmall:  preview,
		WorkshopID:    workshopID,
		WorkshopURL:   workshopURL,
		ItemID:        workshopID,
		ContentRating: contentRating,
		Tags:          tags,
		Status:        "installed",
		Local:         local,
		Approved:      true,
		General:       meta.General,
		Properties:    meta.Properties,
	}, true
}
