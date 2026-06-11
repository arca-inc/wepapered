package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// WEMessage is sent by the WE UI spy to our daemon.
type WEMessage struct {
	Object   string        `json:"object"`
	Method   string        `json:"method"`
	Args     []interface{} `json:"args"`
	Callback string        `json:"callback"`
	Type     string        `json:"type"`
	Msg      string        `json:"msg"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WSServer struct {
	mu           sync.RWMutex
	clients      map[*websocket.Conn]struct{}
	cfg          *Config
	state        *DaemonState
	monitorInfos []MonitorInfo // populated from applyGeneral
	renderer     *Renderer
	discord      *DiscordRP
}

func newWSServer(cfg *Config) *WSServer {
	return &WSServer{
		clients:  make(map[*websocket.Conn]struct{}),
		cfg:      cfg,
		state:    loadState(),
		renderer: newRenderer(cfg),
		discord:  newDiscordRP(),
	}
}

// updateDiscordPresence sets the Discord Rich Presence. The app name ("WePapered",
// from the Discord app registry) renders above the details line, giving:
//   WePapered
//   Patched for Linux
func (s *WSServer) updateDiscordPresence() {
	if s.discord == nil {
		return
	}
	s.discord.SetActivity("Patched for Linux", "")
}

func (s *WSServer) Start(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/we", s.handle)
	mux.HandleFunc("/ui/", s.serveUI)
	localeHandler := func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path[strings.LastIndex(r.URL.Path, "locale/")+len("locale/"):]
		lang := strings.TrimSuffix(reqPath, ".json")
		log.Printf("[wepapered] UI requested locale: %s", lang)
		if table := loadLocale(s.cfg.WEPath, lang); len(table) > 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(table)
			return
		}
		filePath := filepath.Join(s.cfg.WEPath, "locale", reqPath)
		http.ServeFile(w, r, filePath)
	}
	mux.HandleFunc("/locale/", localeHandler)
	mux.HandleFunc("/ui/locale/", localeHandler)
	mux.HandleFunc("/steamapps/", func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path[len("/steamapps/"):]
		steamappsPath := filepath.Join(s.cfg.WEPath, "..", "..")
		filePath := filepath.Join(steamappsPath, reqPath)
		http.ServeFile(w, r, filePath)
	})
	go func() {
		log.Printf("[wepapered] WS server on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[wepapered] WS error: %v", err)
		}
	}()
}

func (s *WSServer) handle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.clients[conn] = struct{}{}
	s.mu.Unlock()
	log.Printf("[wepapered] UI connected (%s)", r.RemoteAddr)

	// Send current state immediately so the inject script can restore active wallpapers.
	if stateData, err := json.Marshal(map[string]interface{}{
		"type":  "state",
		"state": s.state,
	}); err == nil {
		conn.WriteMessage(websocket.TextMessage, stateData)
	}

	// Push the library + translations so a hosted UI client (CEF/probe) can
	// populate the browse grid with no Wallpaper Engine process. Harmless to the
	// legacy inject spy, which ignores unknown message types.
	s.sendHostedUIData(conn)

	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
		log.Printf("[wepapered] UI disconnected")
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg WEMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		s.dispatch(conn, msg)
	}
}

func (s *WSServer) dispatch(conn *websocket.Conn, msg WEMessage) {
	if msg.Type == "log" {
		log.Printf("[JS] %s", msg.Msg)
		return
	}
	if msg.Callback != "" {
		s.handleCallbackMessage(conn, msg)
		return
	}

	switch msg.Object {
	case "browseWallpaperObject":
		s.handleBrowse(conn, msg)
	case "settingsObject":
		s.handleSettings(conn, msg)
	default:
		log.Printf("[WE→] %s.%s", msg.Object, msg.Method)
	}
}

func (s *WSServer) handleCallbackMessage(conn *websocket.Conn, msg WEMessage) {
	log.Printf("[WE←C++] Object=%s Method=%s callback=%s", msg.Object, msg.Method, msg.Callback)
	
	// Try to reply to getMonitors if it asks
	if msg.Object == "browseWallpaperObject" && (msg.Method == "getMonitors" || msg.Method == "getDisplays") {
		var monitorsArray []map[string]interface{}
		loc := 0
		for label := range s.state.Monitors {
			idx := loc
			fmt.Sscanf(label, "Monitor%d", &idx)
			monitorsArray = append(monitorsArray, map[string]interface{}{
				"index":      idx,
				"location":   idx,
				"name":       label,
				"devicePath": label,
				"deviceName": label,
				"isClone":    false,
				"isInGroup":  false,
				"x0":         idx * 1920,
				"y0":         0,
				"x1":         (idx + 1) * 1920,
				"y1":         1080,
			})
			loc++
		}
		
		reply, _ := json.Marshal(map[string]interface{}{
			"callback": msg.Callback,
			"args": []interface{}{ monitorsArray },
		})
		conn.WriteMessage(websocket.TextMessage, reply)
	}
}

func (s *WSServer) handleBrowse(conn *websocket.Conn, msg WEMessage) {
	switch msg.Method {
	case "selectWallpaper":
		s.onSelectWallpaper(msg.Args)
	case "persistUserMonitorSettings":
		log.Printf("[WE] monitor settings persisted")
	case "persistBrowserSettings":
		log.Printf("[WE] browser settings persisted")
	case "updateProfile":
		log.Printf("[WE] updateProfile intercepted — callback will be wrapped")
	default:
		log.Printf("[WE] browse.%s", msg.Method)
	}
}

func (s *WSServer) handleSettings(conn *websocket.Conn, msg WEMessage) {
	switch msg.Method {
	case "applyGeneral":
		if len(msg.Args) > 0 {
			if payload, ok := msg.Args[0].(map[string]interface{}); ok {
				if mm, ok := payload["monitormap"]; ok {
					s.monitorInfos = parseMonitorMap(mm)
					log.Printf("[WE] monitormap updated: %d monitors", len(s.monitorInfos))
				}
			}
		}
	default:
		log.Printf("[WE] settings.%s", msg.Method)
	}
}

func (s *WSServer) onSelectWallpaper(args []interface{}) {
	if len(args) < 2 {
		return
	}
	winPath, _ := args[0].(string)
	
	monitor := ""
	switch v := args[1].(type) {
	case string:
		monitor = v
	case float64:
		monitor = fmt.Sprintf("%d", int(v))
	case map[string]interface{}:
		if loc, ok := v["location"].(float64); ok {
			monitor = fmt.Sprintf("%d", int(loc))
		} else if locStr, ok := v["location"].(string); ok {
			monitor = locStr
		}
	}
	
	if _, err := strconv.Atoi(monitor); err == nil {
		monitor = "Monitor" + monitor
	}

	if winPath == "" || monitor == "" {
		log.Printf("[WE] ERROR: selectWallpaper ignored (winPath=%q, monitor=%q, rawArg1=%v)", winPath, monitor, args[1])
		return
	}

	// The hosted UI sends the Linux directory path directly; the legacy WE inject
	// spy sends a Windows path (S:/…, Z:/…) that needs translation.
	linuxPath := winPath
	if len(winPath) == 0 || winPath[0] != '/' {
		linuxPath = winToLinux(winPath, s.cfg.WEPath)
	}
	workshopID := workshopIDFromPath(winPath)
	meta := readProjectMeta(linuxPath)

	// If type is still unknown, try the dependency workshop item.
	renderDir := ""
	presetDir := ""
	var props map[string]string
	if meta != nil && meta.Type == "" && meta.Dependency != "" {
		dir := linuxPath
		if !isDir(dir) {
			dir = filepath.Dir(dir)
		}
		depDir := filepath.Join(filepath.Dir(dir), meta.Dependency)
		if depMeta := readProjectMeta(filepath.Join(depDir, "project.json")); depMeta != nil && depMeta.Type != "" {
			meta.Type = depMeta.Type
			renderDir = depDir
			presetDir = dir // the original wallpaper dir holds assets (directories/, files/)
			props = presetToStringMap(meta.Preset)
			log.Printf("[wepapered] inferred type %q from dependency %s", meta.Type, meta.Dependency)
		}
	}

	// Resolve device path for this monitor label (e.g. Monitor0 → location 0 → device path)
	devicePath := ""
	loc := -1
	fmt.Sscanf(monitor, "Monitor%d", &loc)
	for _, mi := range s.monitorInfos {
		if mi.Location == loc {
			devicePath = mi.DevicePath
			break
		}
	}

	mw := &MonitorWallpaper{
		Monitor:    monitor,
		WinPath:    winPath,
		LinuxPath:  linuxPath,
		WorkshopID: workshopID,
		DevicePath: devicePath,
		RenderDir:  renderDir,
		PresetDir:  presetDir,
		Props:      props,
	}
	if meta != nil {
		mw.Title = meta.Title
		mw.Type = meta.Type
		mw.PreviewFile = meta.Preview
	}

	s.state.Monitors[monitor] = mw
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}

	// Write back into WE's config.json so it remembers on next startup.
	if err := writeWESelectedWallpapers(s.cfg.WEPath, s.state.Monitors, s.monitorInfos); err != nil {
		log.Printf("[wepapered] WE config write error: %v", err)
	}

	log.Printf("[WE] *** %s → %s (%s) [%s]", monitor, mw.Title, mw.Type, workshopID)
	notifyUser(fmt.Sprintf("%s: %s", monitor, mw.Title))
	s.updateDiscordPresence()

	// Apply wallpapers via linux-wallpaperengine.
	go s.renderer.Apply(s.state)
}

// sendHostedUIData pushes the wallpaper library and the translation table to a
// freshly connected client. Used by the hosted-UI mode (CEF window / probe).
func (s *WSServer) sendHostedUIData(conn *websocket.Conn) {
	lib := enumerateLibrary(s.cfg.WEPath)
	if data, err := json.Marshal(map[string]interface{}{
		"type":       "library",
		"wallpapers": lib,
	}); err == nil {
		conn.WriteMessage(websocket.TextMessage, data)
	}

	locale := loadLocale(s.cfg.WEPath, "en-us")
	if len(locale) > 0 {
		if data, err := json.Marshal(map[string]interface{}{
			"type":  "locale",
			"table": locale,
		}); err == nil {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

// Broadcast sends a message to all connected WE UI clients.
func (s *WSServer) Broadcast(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn := range s.clients {
		conn.WriteMessage(websocket.TextMessage, data)
	}
}

func notifyUser(body string) {
	var cmd *exec.Cmd
	if os.Getuid() == 0 {
		dbusAddr := fmt.Sprintf("unix:path=/run/user/%d/bus", sessionUID())
		cmd = exec.Command("sudo", "-u", sessionUsername(),
			"env", "DBUS_SESSION_BUS_ADDRESS="+dbusAddr,
			"notify-send", "-a", "wepapered", "Wallpaper Engine", body)
	} else {
		cmd = exec.Command("notify-send", "-a", "wepapered", "Wallpaper Engine", body)
	}
	if err := cmd.Run(); err != nil {
		log.Printf("[wepapered] notify failed: %v", err)
	}
}
