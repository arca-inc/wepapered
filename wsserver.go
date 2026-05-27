package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
}

func newWSServer(cfg *Config) *WSServer {
	return &WSServer{
		clients:  make(map[*websocket.Conn]struct{}),
		cfg:      cfg,
		state:    loadState(),
		renderer: newRenderer(cfg),
	}
}

func (s *WSServer) Start(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/we", s.handle)
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
		log.Printf("[WE←C++] callback=%s", msg.Callback)
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
	monitor, _ := args[1].(string)
	if winPath == "" || monitor == "" {
		return
	}

	linuxPath := winToLinux(winPath, s.cfg.WEPath)
	workshopID := workshopIDFromPath(winPath)
	meta := readProjectMeta(linuxPath)

	// If type is still unknown, try the dependency workshop item.
	renderDir := ""
	if meta != nil && meta.Type == "" && meta.Dependency != "" {
		dir := linuxPath
		if !isDir(dir) {
			dir = filepath.Dir(dir)
		}
		depDir := filepath.Join(filepath.Dir(dir), meta.Dependency)
		if depMeta := readProjectMeta(filepath.Join(depDir, "project.json")); depMeta != nil && depMeta.Type != "" {
			meta.Type = depMeta.Type
			renderDir = depDir
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

	// Apply wallpapers via linux-wallpaperengine.
	go s.renderer.Apply(s.state)
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
