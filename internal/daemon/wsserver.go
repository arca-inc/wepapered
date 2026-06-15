package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	mu      sync.RWMutex
	clients map[*websocket.Conn]struct{}
	// cfg is an atomic pointer so /reload can publish a new config (copy-on-write)
	// without racing the many lock-free readers. Read with s.cfg.Load().
	cfg          atomic.Pointer[Config]
	state        *DaemonState
	monitorInfos []MonitorInfo // populated from applyGeneral
	renderer     *Renderer
	watcher      *Watcher // set in run.go; lets reload re-point the config watch
	discord      *DiscordRP
	playlists    *PlaylistEngine // per-monitor wallpaper rotation

	// stateMu guards all access to s.state (its maps and fields) and serializes the
	// persist+snapshot sequence, since the playlist engine mutates state from timer
	// goroutines concurrently with the websocket dispatch goroutines.
	stateMu sync.Mutex

	favMu sync.Mutex // serializes favorite read-modify-write of the config

	// Debounce for property edits: dragging a slider fires many applySingleProperty
	// calls; state is updated immediately but the renderer re-apply (reload) coalesces.
	applyDebMu    sync.Mutex
	applyDebTimer *time.Timer

	// sessionBaseline is a snapshot of the state when the browse UI last opened,
	// used to roll back on Cancel (cancelAndClose).
	sessionBaseline *DaemonState

	// Debounce state for queryWorkshop (see handleQueryWorkshop).
	qwMu    sync.Mutex
	qwTimer *time.Timer
	qwConn  *websocket.Conn
	qwMsg   WEMessage
}

func newWSServer(cfg *Config) *WSServer {
	s := &WSServer{
		clients:  make(map[*websocket.Conn]struct{}),
		state:    loadState(),
		renderer: newRenderer(cfg),
		discord:  newDiscordRP(),
	}
	s.cfg.Store(cfg)
	s.playlists = newPlaylistEngine(s)
	return s
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

func (s *WSServer) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/we", s.handle)
	mux.HandleFunc("/reload", s.handleReload)
	mux.HandleFunc("/ui/", s.serveUI)
	localeHandler := func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path[strings.LastIndex(r.URL.Path, "locale/")+len("locale/"):]
		lang := strings.TrimSuffix(reqPath, ".json")
		log.Printf("[wepapered] UI requested locale: %s", lang)
		if table := loadLocale(s.cfg.Load().WEPath, lang); len(table) > 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(table)
			return
		}
		filePath := filepath.Join(s.cfg.Load().WEPath, "locale", reqPath)
		http.ServeFile(w, r, filePath)
	}
	mux.HandleFunc("/locale/", localeHandler)
	mux.HandleFunc("/ui/locale/", localeHandler)
	mux.HandleFunc("/steamapps/", func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path[len("/steamapps/"):]
		steamappsPath := filepath.Join(s.cfg.Load().WEPath, "..", "..")
		filePath := filepath.Join(steamappsPath, reqPath)
		http.ServeFile(w, r, filePath)
	})
	mux.HandleFunc("/projects/", func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path[len("/projects/"):]
		filePath := filepath.Join(s.cfg.Load().WEPath, "projects", reqPath)
		http.ServeFile(w, r, filePath)
	})
	// /asset serves a preview by absolute path, restricted to known roots (the WE
	// install, configured custom dirs, Steam libraries). Used for wallpapers that
	// live outside the /projects and /steamapps trees (custom dirs).
	mux.HandleFunc("/asset/", func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(r.URL.Path[len("/asset"):]) // keeps the leading slash
		roots := append([]string{s.cfg.Load().WEPath}, s.cfg.Load().CustomDirs...)
		roots = append(roots, steamLibraryDirs()...)
		allowed := false
		for _, root := range roots {
			if root == "" {
				continue
			}
			root = filepath.Clean(root)
			if p == root || strings.HasPrefix(p, root+string(os.PathSeparator)) {
				allowed = true
				break
			}
		}
		if !allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.ServeFile(w, r, p)
	})
	// Bind synchronously so the caller can detect "port already in use" (another
	// instance) and refuse to start a second, competing daemon.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		log.Printf("[wepapered] WS server on %s", addr)
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("[wepapered] WS error: %v", err)
		}
	}()
	return nil
}

// handleReload re-reads the on-disk config and relaunches the renderers so settings
// changes (audio device, preferred player, now-playing text, theme, custom dirs, …)
// take effect without restarting the daemon. Triggered by `wepaperedctl reload` and by
// the settings window on save.
func (s *WSServer) handleReload(w http.ResponseWriter, r *http.Request) {
	newCfg, err := loadConfig()
	if err != nil {
		log.Printf("[wepapered] reload: config load error: %v", err)
		http.Error(w, "config load error", http.StatusInternalServerError)
		return
	}
	// Repair the WE path the same way startup does, so a bad saved path doesn't break.
	if !weDirValid(newCfg.WEPath) {
		if resolved := resolveWEPath(newCfg); resolved != "" {
			newCfg.WEPath = resolved
		}
	}
	// Copy-on-write: publish the fully-built newCfg to each subsystem by swapping
	// pointers, never mutating the shared struct in place. A pointer swap is a single
	// word write, so lock-free readers always observe a complete config (old or new),
	// never a half-overwritten one with a torn slice/string header.
	s.cfg.Store(newCfg)
	s.renderer.SetConfig(newCfg)
	if s.watcher != nil {
		s.watcher.Rebind(newCfg.WEPath)
	}

	log.Printf("[wepapered] reload requested — reloading config and relaunching renderers")
	s.stateMu.Lock()
	reloadSnap := s.state.snapshot()
	s.stateMu.Unlock()
	go s.renderer.Reload(reloadSnap)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "reloaded")
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

	// Snapshot the state as the rollback baseline for this browse session (Cancel),
	// and as a stable value to marshal (the playlist engine may mutate s.state from
	// timer goroutines concurrently).
	s.stateMu.Lock()
	s.sessionBaseline = s.state.snapshot()
	stateData, _ := json.Marshal(map[string]interface{}{
		"type":  "state",
		"state": s.state,
	})
	s.stateMu.Unlock()

	// Send current state immediately so the inject script can restore active wallpapers.
	if stateData != nil {
		conn.WriteMessage(websocket.TextMessage, stateData)
	}

	// Push the real displays so the hosted UI's "Choose display" picker is
	// populated before any wallpaper is assigned (otherwise it has nothing to
	// select, and a wallpaper can't be applied without a selected monitor).
	if outs, err := hyprlandOutputs(); err == nil && len(outs) > 0 {
		var disp []map[string]interface{}
		for i, o := range outs {
			w, h := o.Width, o.Height
			if w == 0 {
				w = 1920
			}
			if h == 0 {
				h = 1080
			}
			disp = append(disp, map[string]interface{}{
				"index":      i,
				"location":   i,
				"name":       fmt.Sprintf("Monitor%d", i),
				"deviceName": o.Name,
				"devicePath": fmt.Sprintf("Monitor%d", i),
				"isClone":    false,
				"isInGroup":  false,
				"x0":         o.X,
				"y0":         o.Y,
				"x1":         o.X + w,
				"y1":         o.Y + h,
			})
		}
		if data, err := json.Marshal(map[string]interface{}{
			"type":     "displays",
			"displays": disp,
		}); err == nil {
			conn.WriteMessage(websocket.TextMessage, data)
		}
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
		// If it's a getMonitors or getDisplays call, handleCallbackMessage handles it, but other methods also have callbacks now!
	}

	switch msg.Object {
	case "browseWallpaperObject":
		s.handleBrowse(conn, msg)
	case "settingsObject":
		s.handleSettings(conn, msg)
	case "installObject":
		s.handleInstall(conn, msg)
	case "ui":
		s.handleUI(conn, msg)
	default:
		log.Printf("[WE→] %s.%s (callback=%s)", msg.Object, msg.Method, msg.Callback)
	}
}

func (s *WSServer) handleCallbackMessage(conn *websocket.Conn, msg WEMessage) {
	log.Printf("[WE←C++] Object=%s Method=%s callback=%s", msg.Object, msg.Method, msg.Callback)

	if msg.Object == "browseWallpaperObject" && (msg.Method == "getMonitors" || msg.Method == "getDisplays") {
		var monitorsArray []map[string]interface{}

		// Use real Hyprland output geometry when available.
		outputs, err := hyprlandOutputs()
		if err == nil && len(outputs) > 0 {
			for idx, o := range outputs {
				label := fmt.Sprintf("Monitor%d", idx)
				w, h := o.Width, o.Height
				if w == 0 { w = 1920 }
				if h == 0 { h = 1080 }
				monitorsArray = append(monitorsArray, map[string]interface{}{
					"index":      idx,
					"location":   idx,
					"name":       label,
					"devicePath": label,
					"deviceName": o.Name,
					"isClone":    false,
					"isInGroup":  false,
					"x0":         o.X,
					"y0":         o.Y,
					"x1":         o.X + w,
					"y1":         o.Y + h,
				})
			}
		} else {
			// Fallback: build from saved state labels.
			s.stateMu.Lock()
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
			s.stateMu.Unlock()
		}

		reply, _ := json.Marshal(map[string]interface{}{
			"callback": msg.Callback,
			"args":     []interface{}{monitorsArray},
		})
		conn.WriteMessage(websocket.TextMessage, reply)
	}
}

func (s *WSServer) handleBrowse(conn *websocket.Conn, msg WEMessage) {
	switch msg.Method {
	case "selectWallpaper":
		s.onSelectWallpaper(msg.Args)
	case "selectPlaylist":
		s.onSelectPlaylist(msg.Args)
	case "removeWallpaper":
		s.onRemoveWallpaper(msg.Args)
	case "playlistsChanged":
		s.onPlaylistsChanged(msg.Args)
	case "profilesChanged":
		s.onProfilesChanged(msg.Args)
	case "transferWallpaperProperties":
		s.onTransferWallpaper(msg.Args)
	case "applySingleProperty":
		s.onApplyProperty(msg.Args)
	case "resetWallpaperLocalStorage":
		s.onResetProperties(msg.Args)
	case "queryWorkshop":
		s.handleQueryWorkshop(conn, msg)
	case "persistUserMonitorSettings":
		log.Printf("[WE] monitor settings persisted")
	case "persistBrowserSettings":
		s.onPersistBrowserSettings(msg.Args)
	case "changeLayout":
		s.onChangeLayout(msg.Args)
	case "acceptAndClose":
		s.onAcceptAndClose()
	case "cancelAndClose":
		s.onCancelAndClose()
	case "openInExplorer":
		s.onOpenInExplorer(msg.Args)
	case "showSettingsDialog":
		s.openConfigWindow()
	case "updateProfile":
		s.onUpdateProfile(msg.Args)
	default:
		log.Printf("[WE] browse.%s", msg.Method)
	}
}

// layoutClone is WE's "Clone single wallpaper" layout: the same wallpaper is
// shown on every display.
const layoutClone = 2

// onChangeLayout records WE's wallpaper layout mode and, when switching to clone,
// immediately mirrors the current selection across all outputs.
func (s *WSServer) onChangeLayout(args []interface{}) {
	if len(args) == 0 {
		return
	}
	v, ok := args[0].(float64)
	if !ok {
		return
	}
	s.stateMu.Lock()
	s.state.Layout = int(v)
	log.Printf("[WE] layout → %d", s.state.Layout)
	// Re-clone the current wallpaper onto every display right away so toggling to
	// clone mode takes effect without re-picking.
	apply := false
	if s.state.Layout == layoutClone {
		if src := s.anyMonitorWallpaper(); src != nil {
			s.cloneToAllOutputs(src)
			apply = true
		}
	}
	s.persistLocked()
	snap := s.state.snapshot()
	s.stateMu.Unlock()
	if apply {
		go s.renderer.Apply(snap)
	}
}

// allMonitorLabels returns the Monitor0..N labels for the current real outputs,
// falling back to whatever labels are already in state if hyprctl is unavailable.
func (s *WSServer) allMonitorLabels() []string {
	if outs, err := hyprlandOutputs(); err == nil && len(outs) > 0 {
		labels := make([]string, len(outs))
		for i := range outs {
			labels[i] = fmt.Sprintf("Monitor%d", i)
		}
		return labels
	}
	var labels []string
	for k := range s.state.Monitors {
		labels = append(labels, k)
	}
	return labels
}

// devicePathFor resolves the WE device path for a Monitor label via the
// monitormap, or "" if unknown.
func (s *WSServer) devicePathFor(label string) string {
	loc := -1
	fmt.Sscanf(label, "Monitor%d", &loc)
	for _, mi := range s.monitorInfos {
		if mi.Location == loc {
			return mi.DevicePath
		}
	}
	return ""
}

// anyMonitorWallpaper returns one currently-assigned wallpaper (preferring
// Monitor0), used as the source when cloning, or nil if nothing is assigned.
func (s *WSServer) anyMonitorWallpaper() *MonitorWallpaper {
	if mw := s.state.Monitors["Monitor0"]; mw != nil {
		return mw
	}
	for _, mw := range s.state.Monitors {
		if mw != nil {
			return mw
		}
	}
	return nil
}

// cloneToAllOutputs assigns a copy of src to every output, each carrying its own
// Monitor label and device path.
func (s *WSServer) cloneToAllOutputs(src *MonitorWallpaper) {
	for _, label := range s.allMonitorLabels() {
		c := *src
		c.Monitor = label
		c.DevicePath = s.devicePathFor(label)
		s.state.Monitors[label] = &c
	}
}

// onOpenInExplorer opens the wallpaper's folder in the user's file manager. The arg is
// the wallpaper's project/file path (a Linux path from the hosted UI, or a Windows path
// from the legacy WE spy). Opens the containing directory if the path is a file.
func (s *WSServer) onOpenInExplorer(args []interface{}) {
	if len(args) == 0 {
		return
	}
	p, _ := args[0].(string)
	if p == "" {
		return
	}
	if p[0] != '/' {
		p = winToLinux(p, s.cfg.Load().WEPath)
	}
	dir := p
	if !isDir(dir) {
		dir = filepath.Dir(dir)
	}
	log.Printf("[WE] openInExplorer → xdg-open %s", dir)
	exec.Command("xdg-open", dir).Start() //nolint
}

// openConfigWindow launches the wepapered-settings GTK window. Triggered by the
// WE UI's Settings button (showSettingsDialog). Settings is its own binary since
// the four-binary split, so launch the sibling binary (not the daemon itself).
// GTK3 runs under Wayland, so this can spawn from the daemon process directly.
func (s *WSServer) openConfigWindow() {
	bin := siblingBinary(settingsBinary)
	if bin == "" {
		log.Printf("[wepapered] settings binary (%s) not found", settingsBinary)
		return
	}
	cmd := exec.Command(bin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[wepapered] failed to open settings window: %v", err)
		return
	}
	go cmd.Wait()
	log.Printf("[WE] showSettingsDialog → launched %s", settingsBinary)
}

// onPersistBrowserSettings saves WE's browserSettings object (sent as a JSON
// string) so UI preferences like "Show on start" survive restarts. Restored into
// the UI by updateUIState in the inject shim.
func (s *WSServer) onPersistBrowserSettings(args []interface{}) {
	if len(args) == 0 {
		return
	}
	var raw json.RawMessage
	switch v := args[0].(type) {
	case string:
		raw = json.RawMessage(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		raw = b
	}
	if !json.Valid(raw) {
		log.Printf("[WE] persistBrowserSettings: invalid JSON, ignored")
		return
	}
	s.stateMu.Lock()
	s.state.BrowserSettings = raw
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	s.stateMu.Unlock()
	log.Printf("[WE] browser settings persisted (%d bytes)", len(raw))
}

// onAcceptAndClose commits the current selections (OK button): persist state and
// re-write WE's config, and adopt the current state as the new rollback baseline.
func (s *WSServer) onAcceptAndClose() {
	s.stateMu.Lock()
	s.persistLocked()
	s.sessionBaseline = s.state.snapshot()
	s.stateMu.Unlock()
	log.Printf("[WE] acceptAndClose — settings saved")
}

// onCancelAndClose rolls back to the baseline captured when the browse UI opened
// (Cancel button), re-rendering the original wallpapers. Best-effort: if no
// baseline was captured, nothing changes.
func (s *WSServer) onCancelAndClose() {
	s.stateMu.Lock()
	if s.sessionBaseline == nil {
		s.stateMu.Unlock()
		log.Printf("[WE] cancelAndClose — no baseline, nothing to roll back")
		return
	}
	restored := s.sessionBaseline.snapshot() // independent copy; keep baseline intact
	s.state.Monitors = restored.Monitors
	s.state.MonitorPlaylists = restored.MonitorPlaylists
	s.state.Layout = restored.Layout
	s.persistLocked()
	s.stateMu.Unlock()

	// Restart rotation from the restored playlists (timers created during the
	// cancelled session are stopped; the restored ones are re-armed).
	s.playlists.Stop()
	s.playlists.StartAll()

	s.stateMu.Lock()
	snap := s.state.snapshot()
	s.stateMu.Unlock()
	log.Printf("[WE] cancelAndClose — rolled back to baseline")
	go s.renderer.Apply(snap)
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

// monitorLabelFromArg normalizes the WE selectWallpaper monitor argument (a label
// string, a numeric location, or a {location} object) to a "MonitorN" label.
func monitorLabelFromArg(arg interface{}) string {
	monitor := ""
	switch v := arg.(type) {
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
	return monitor
}

// resolveWallpaper turns a wallpaper path (a Linux directory from the hosted UI, or
// a Windows S:/Z: path from the legacy spy) plus a target monitor label into a fully
// resolved *MonitorWallpaper (type, render/preset dirs, props, device path). Returns
// nil for an empty path. Pure: no state mutation, no lock — safe to call from the
// playlist engine while resolving items.
func (s *WSServer) resolveWallpaper(winPath, monitor string) *MonitorWallpaper {
	if winPath == "" {
		return nil
	}
	// The hosted UI sends the Linux directory path directly; the legacy WE inject
	// spy sends a Windows path (S:/…, Z:/…) that needs translation.
	linuxPath := winPath
	if winPath[0] != '/' {
		linuxPath = winToLinux(winPath, s.cfg.Load().WEPath)
	}
	workshopID := workshopIDFromPath(winPath)
	meta := readProjectMeta(linuxPath)

	// If type is still unknown, try the dependency workshop item (preset wallpaper).
	renderDir := ""
	presetDir := ""
	var props map[string]string
	if meta != nil && meta.Type == "" && meta.Dependency != "" {
		dir := linuxPath
		if !isDir(dir) {
			dir = filepath.Dir(dir)
		}
		depDir := filepath.Join(filepath.Dir(dir), string(meta.Dependency))
		if depMeta := readProjectMeta(filepath.Join(depDir, "project.json")); depMeta != nil && depMeta.Type != "" {
			meta.Type = depMeta.Type
			renderDir = depDir
			presetDir = dir // the original wallpaper dir holds assets (directories/, files/)
			props = presetToStringMap(meta.Preset)
			log.Printf("[wepapered] inferred type %q from dependency %s", meta.Type, meta.Dependency)
		}
	}

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
	return mw
}

// persistLocked saves state.json and mirrors the current per-monitor wallpapers
// into WE's config.json. Caller must hold stateMu.
func (s *WSServer) persistLocked() {
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	if err := writeWESelectedWallpapers(s.cfg.Load().WEPath, s.state.Monitors, s.monitorInfos); err != nil {
		log.Printf("[wepapered] WE config write error: %v", err)
	}
}

func (s *WSServer) onSelectWallpaper(args []interface{}) {
	if len(args) < 2 {
		return
	}
	winPath, _ := args[0].(string)
	monitor := monitorLabelFromArg(args[1])
	if winPath == "" || monitor == "" {
		log.Printf("[WE] ERROR: selectWallpaper ignored (winPath=%q, monitor=%q, rawArg1=%v)", winPath, monitor, args[1])
		return
	}

	mw := s.resolveWallpaper(winPath, monitor)
	if mw == nil {
		return
	}

	s.stateMu.Lock()
	// A single selection replaces any playlist that was rotating this monitor.
	s.playlists.stopTimer(monitor)
	delete(s.state.MonitorPlaylists, monitor)
	if s.state.Layout == layoutClone {
		// Clone mode mirrors one wallpaper everywhere, so it also retires every
		// other monitor's playlist.
		for _, label := range s.allMonitorLabels() {
			if label != monitor {
				s.playlists.stopTimer(label)
				delete(s.state.MonitorPlaylists, label)
			}
		}
		s.cloneToAllOutputs(mw)
	} else {
		s.state.Monitors[monitor] = mw
	}
	s.persistLocked()
	snap := s.state.snapshot()
	s.stateMu.Unlock()

	log.Printf("[WE] *** %s → %s (%s) [%s]", monitor, mw.Title, mw.Type, mw.WorkshopID)
	notifyUser(fmt.Sprintf("%s: %s", monitor, mw.Title))
	s.updateDiscordPresence()

	// Apply wallpapers via linux-wallpaperengine.
	go s.renderer.Apply(snap)
}

// parsePlaylistSettings reads WE's playlist `settings` object, defaulting to a
// random 60-minute timer.
func parsePlaylistSettings(s map[string]interface{}) PlaylistSettings {
	ps := PlaylistSettings{Order: "random", Mode: "timer", Delay: 60}
	if v, ok := s["delay"].(float64); ok {
		ps.Delay = int(v)
	}
	if v, ok := s["order"].(string); ok && v != "" {
		ps.Order = v
	}
	if v, ok := s["mode"].(string); ok && v != "" {
		ps.Mode = v
	}
	if v, ok := s["transition"].(string); ok {
		ps.Transition = v
	}
	if v, ok := s["transitiontime"].(float64); ok {
		ps.TransitionTime = int(v)
	}
	if v, ok := s["videosequence"].(bool); ok {
		ps.VideoSequence = v
	}
	if v, ok := s["updateonpause"].(bool); ok {
		ps.UpdateOnPause = v
	}
	return ps
}

// parseMonitorPlaylist converts WE's serialized playlist ({name, settings, items})
// into a *MonitorPlaylist. items are either a bare file string or {file, daytimeend,
// preset}.
func parseMonitorPlaylist(raw interface{}) *MonitorPlaylist {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	pl := &MonitorPlaylist{}
	if name, ok := m["name"].(string); ok {
		pl.Name = name
	}
	if s, ok := m["settings"].(map[string]interface{}); ok {
		pl.Settings = parsePlaylistSettings(s)
	} else {
		pl.Settings = PlaylistSettings{Order: "random", Mode: "timer", Delay: 60}
	}
	items, _ := m["items"].([]interface{})
	for _, it := range items {
		switch v := it.(type) {
		case string:
			if v != "" {
				pl.Items = append(pl.Items, PlaylistItem{File: v})
			}
		case map[string]interface{}:
			pi := PlaylistItem{}
			if f, ok := v["file"].(string); ok {
				pi.File = f
			}
			if d, ok := v["daytimeend"].(float64); ok {
				pi.DaytimeEnd = d
			}
			if p, ok := v["preset"].(string); ok {
				pi.Preset = p
			}
			if pi.File != "" {
				pl.Items = append(pl.Items, pi)
			}
		}
	}
	return pl
}

// onSelectPlaylist handles browseWallpaperObject.selectPlaylist(serialized, location):
// install a rotating playlist on a monitor. A ≤1-item playlist is not a playlist
// (mirrors the WE UI) and collapses to a single selection.
func (s *WSServer) onSelectPlaylist(args []interface{}) {
	if len(args) < 2 {
		return
	}
	pl := parseMonitorPlaylist(args[0])
	monitor := monitorLabelFromArg(args[1])
	if pl == nil || monitor == "" {
		return
	}
	if len(pl.Items) <= 1 {
		s.stateMu.Lock()
		s.playlists.stopTimer(monitor)
		delete(s.state.MonitorPlaylists, monitor)
		s.stateMu.Unlock()
		if len(pl.Items) == 1 {
			s.onSelectWallpaper([]interface{}{pl.Items[0].File, args[1]})
		}
		return
	}
	s.playlists.SetPlaylist(monitor, pl)
	notifyUser(fmt.Sprintf("%s: playlist (%d wallpapers)", monitor, len(pl.Items)))
	s.updateDiscordPresence()
}

// onRemoveWallpaper handles browseWallpaperObject.removeWallpaper({location, playlist}):
// clear a monitor's wallpaper and/or playlist and stop rendering it.
func (s *WSServer) onRemoveWallpaper(args []interface{}) {
	if len(args) == 0 {
		return
	}
	m, ok := args[0].(map[string]interface{})
	if !ok {
		return
	}
	monitor := monitorLabelFromArg(m["location"])
	if monitor == "" {
		return
	}
	s.stateMu.Lock()
	s.playlists.stopTimer(monitor)
	delete(s.state.MonitorPlaylists, monitor)
	delete(s.state.Monitors, monitor)
	s.persistLocked()
	snap := s.state.snapshot()
	s.stateMu.Unlock()
	log.Printf("[WE] removeWallpaper %s", monitor)
	go s.renderer.Apply(snap)
}

// onPlaylistsChanged persists WE's named-playlist library (the `playlists` array,
// sent as a JSON string) so saved playlists survive restarts and round-trip back
// into the UI.
// rawJSONArg extracts a JSON payload from a bridge arg that may arrive as a JSON
// string (WE's angular.toJson) or as an already-decoded value. Returns false when
// the arg is missing or the result isn't valid JSON.
func rawJSONArg(args []interface{}) (json.RawMessage, bool) {
	if len(args) == 0 {
		return nil, false
	}
	var raw json.RawMessage
	switch v := args[0].(type) {
	case string:
		raw = json.RawMessage(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, false
		}
		raw = b
	}
	if !json.Valid(raw) {
		return nil, false
	}
	return raw, true
}

func (s *WSServer) onPlaylistsChanged(args []interface{}) {
	raw, ok := rawJSONArg(args)
	if !ok {
		log.Printf("[WE] playlistsChanged: invalid JSON, ignored")
		return
	}
	s.stateMu.Lock()
	s.state.SavedPlaylists = raw
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	s.stateMu.Unlock()
	log.Printf("[WE] saved playlists library updated (%d bytes)", len(raw))
}

// onProfilesChanged persists WE's named monitor-profile library (the Save/Load
// profile feature). WE serializes the whole `profiles` array on every change; we
// store it verbatim and feed it back as the config's `profiles` field on load so
// saved profiles survive restarts. Loading a profile re-applies its wallpapers
// through the normal per-monitor selection path, so no extra apply is needed here.
func (s *WSServer) onProfilesChanged(args []interface{}) {
	raw, ok := rawJSONArg(args)
	if !ok {
		log.Printf("[WE] profilesChanged: invalid JSON, ignored")
		return
	}
	s.stateMu.Lock()
	s.state.SavedProfiles = raw
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	s.stateMu.Unlock()
	log.Printf("[WE] saved profiles library updated (%d bytes)", len(raw))
}

// onUpdateProfile persists WE's active monitor arrangement (wallpaperConfig.profile:
// clone groups / splits). Stored verbatim and restored as wallpaperconfig.profile on
// load so the current monitor configuration survives restarts.
func (s *WSServer) onUpdateProfile(args []interface{}) {
	raw, ok := rawJSONArg(args)
	if !ok {
		log.Printf("[WE] updateProfile: invalid JSON, ignored")
		return
	}
	s.stateMu.Lock()
	s.state.MonitorProfile = raw
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	s.stateMu.Unlock()
	log.Printf("[WE] monitor profile updated (%d bytes)", len(raw))
}

// assignLocked sets a monitor's wallpaper and/or playlist to copies of the given
// values, re-targeting the wallpaper to that monitor's label/device path. Caller
// holds stateMu.
func (s *WSServer) assignLocked(label string, wp *MonitorWallpaper, pl *MonitorPlaylist) {
	if wp != nil {
		c := *wp
		c.Monitor = label
		c.DevicePath = s.devicePathFor(label)
		s.state.Monitors[label] = &c
	} else {
		delete(s.state.Monitors, label)
	}
	if pl != nil {
		cp := *pl
		cp.Items = append([]PlaylistItem(nil), pl.Items...)
		s.state.MonitorPlaylists[label] = &cp
	} else {
		delete(s.state.MonitorPlaylists, label)
	}
}

// onTransferWallpaper handles browseWallpaperObject.transferWallpaperProperties
// ({source, destination, swap}): move (or swap) a wallpaper/playlist between
// monitors. Without swap it is a move — the source is cleared.
func (s *WSServer) onTransferWallpaper(args []interface{}) {
	if len(args) == 0 {
		return
	}
	m, ok := args[0].(map[string]interface{})
	if !ok {
		return
	}
	src := monitorLabelFromArg(m["source"])
	dst := monitorLabelFromArg(m["destination"])
	if src == "" || dst == "" || src == dst {
		return
	}
	swap, _ := m["swap"].(bool)

	s.stateMu.Lock()
	srcWp, srcPl := s.state.Monitors[src], s.state.MonitorPlaylists[src]
	dstWp, dstPl := s.state.Monitors[dst], s.state.MonitorPlaylists[dst]
	s.assignLocked(dst, srcWp, srcPl)
	if swap {
		s.assignLocked(src, dstWp, dstPl)
	} else {
		delete(s.state.Monitors, src)
		delete(s.state.MonitorPlaylists, src)
	}
	s.persistLocked()
	snap := s.state.snapshot()
	s.stateMu.Unlock()

	s.playlists.Rearm(dst)
	if swap {
		s.playlists.Rearm(src)
	} else {
		s.playlists.stopTimer(src)
	}
	log.Printf("[WE] transfer %s → %s (swap=%v)", src, dst, swap)
	go s.renderer.Apply(snap)
}

// propValueToString converts a WE property descriptor value to the string form
// linux-wallpaperengine's --set-property expects (color stays "r g b", bool→1/0,
// number→plain). Returns false for values we can't represent (null, objects).
func propValueToString(v interface{}) (string, bool) {
	switch val := v.(type) {
	case bool:
		if val {
			return "1", true
		}
		return "0", true
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64), true
	case string:
		return val, true
	}
	return "", false
}

// scheduleApply debounces a renderer re-apply so a burst of property edits (slider
// drag) coalesces into a single reload.
func (s *WSServer) scheduleApply() {
	s.applyDebMu.Lock()
	if s.applyDebTimer != nil {
		s.applyDebTimer.Stop()
	}
	s.applyDebTimer = time.AfterFunc(250*time.Millisecond, func() {
		s.stateMu.Lock()
		snap := s.state.snapshot()
		s.stateMu.Unlock()
		s.renderer.Apply(snap)
	})
	s.applyDebMu.Unlock()
}

// onApplyProperty handles browseWallpaperObject.applySingleProperty(jsonString):
// the user changed a wallpaper property in the UI. Merge the new value(s) into the
// monitor's Props, persist, and re-render (debounced). The payload is
// {file, location, properties:{key:{value,…}}}.
func (s *WSServer) onApplyProperty(args []interface{}) {
	if len(args) == 0 {
		return
	}
	raw, ok := args[0].(string)
	if !ok {
		b, err := json.Marshal(args[0]) // some clients send an object, not a string
		if err != nil {
			return
		}
		raw = string(b)
	}
	var payload struct {
		File       string                            `json:"file"`
		Location   interface{}                       `json:"location"`
		Properties map[string]struct{ Value interface{} `json:"value"` } `json:"properties"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil || len(payload.Properties) == 0 {
		return
	}
	monitor := monitorLabelFromArg(payload.Location)
	if monitor == "" {
		return
	}

	s.stateMu.Lock()
	mw := s.state.Monitors[monitor]
	if mw == nil {
		s.stateMu.Unlock()
		return
	}
	// Build the merged props map (copy-on-write: the *MonitorWallpaper is shared with
	// the renderer via snapshots and must never be mutated in place).
	merged := make(map[string]string, len(mw.Props)+len(payload.Properties))
	for k, v := range mw.Props {
		merged[k] = v
	}
	for k, d := range payload.Properties {
		if sv, ok := propValueToString(d.Value); ok {
			merged[k] = sv
		}
	}
	apply := func(label string) {
		if cur := s.state.Monitors[label]; cur != nil {
			c := *cur
			c.Props = merged
			s.state.Monitors[label] = &c
		}
	}
	if s.state.Layout == layoutClone {
		for _, label := range s.allMonitorLabels() {
			apply(label)
		}
	} else {
		apply(monitor)
	}
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	s.stateMu.Unlock()

	log.Printf("[WE] property change on %s (%d key(s))", monitor, len(payload.Properties))
	s.scheduleApply()
}

// onResetProperties handles browseWallpaperObject.resetWallpaperLocalStorage(file):
// the user reset a wallpaper's properties. Restore the monitor(s) showing that file
// to the wallpaper's default props (re-resolved from project.json), then re-render.
func (s *WSServer) onResetProperties(args []interface{}) {
	if len(args) == 0 {
		return
	}
	file, _ := args[0].(string)
	if file == "" {
		return
	}
	s.stateMu.Lock()
	changed := false
	for label, mw := range s.state.Monitors {
		if mw == nil || (mw.WinPath != file && mw.LinuxPath != file) {
			continue
		}
		c := *mw
		if def := s.resolveWallpaper(mw.WinPath, label); def != nil {
			c.Props = def.Props
		} else {
			c.Props = nil
		}
		s.state.Monitors[label] = &c
		changed = true
	}
	if changed {
		if err := saveState(s.state); err != nil {
			log.Printf("[wepapered] state save error: %v", err)
		}
	}
	s.stateMu.Unlock()
	if changed {
		log.Printf("[WE] reset properties for %s", file)
		s.scheduleApply()
	}
}

// sendHostedUIData pushes the wallpaper library and the translation table to a
// freshly connected client. Used by the hosted-UI mode (CEF window / probe).
// markFavorites flags wallpapers whose workshopid is in the persisted favorites
// list so the UI shows them as favorited (filled heart, "favorites only" filter).
// The WE UI keys favorites by workshopid, so an installed item (File=path) and the
// same item in Workshop search (File=id) share one favorite key. File is also
// matched as a fallback for any non-workshop favoriting.
func (s *WSServer) markFavorites(lib []UIWallpaper) {
	fav := s.cfg.Load().Favorites
	if len(fav) == 0 {
		return
	}
	set := make(map[string]bool, len(fav))
	for _, f := range fav {
		set[f] = true
	}
	for i := range lib {
		if (lib[i].WorkshopID != "" && set[lib[i].WorkshopID]) || set[lib[i].File] {
			lib[i].Favorite = true
		}
	}
}

// setFavorites adds/removes wallpapers (by workshopid) from the persisted favorites
// list and writes the config to disk. The grid context menu favorites a batch at
// once. Serialized by favMu.
func (s *WSServer) setFavorites(ids []string, fav bool) {
	if len(ids) == 0 {
		return
	}
	touch := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			touch[id] = true
		}
	}
	if len(touch) == 0 {
		return
	}
	s.favMu.Lock()
	defer s.favMu.Unlock()
	cur := s.cfg.Load()
	out := make([]string, 0, len(cur.Favorites)+len(touch))
	for _, f := range cur.Favorites {
		if !touch[f] { // drop the ids we're touching; re-add below if favoriting
			out = append(out, f)
		}
	}
	if fav {
		for id := range touch {
			out = append(out, id)
		}
	}
	newCfg := *cur
	newCfg.Favorites = out
	s.cfg.Store(&newCfg)
	if err := saveConfig(&newCfg); err != nil {
		log.Printf("[wepapered] favorite save error: %v", err)
	}
	log.Printf("[WE] favorite %v: %v (%d total)", fav, ids, len(out))
}

func (s *WSServer) sendHostedUIData(conn *websocket.Conn) {
	lib := enumerateLibrary(s.cfg.Load().WEPath, s.cfg.Load().CustomDirs)
	s.markFavorites(lib)

	sendLibrary := func(library []UIWallpaper) {
		if data, err := json.Marshal(map[string]interface{}{
			"type":       "library",
			"wallpapers": library,
		}); err == nil {
			s.mu.Lock()
			if _, ok := s.clients[conn]; ok {
				conn.WriteMessage(websocket.TextMessage, data)
			}
			s.mu.Unlock()
		}
	}
	
	sendLibrary(lib)

	go func() {
		var ids []string
		for _, w := range lib {
			if w.WorkshopID != "" && w.WorkshopID != w.Title {
				ids = append(ids, w.WorkshopID)
			}
		}
		
		authors := fetchAuthors(ids)
		updated := false
		for i, w := range lib {
			if author, ok := authors[w.WorkshopID]; ok {
				lib[i].Author = author
				updated = true
			}
		}
		
		if updated {
			sendLibrary(lib)
		}
	}()

	locale := loadLocale(s.cfg.Load().WEPath, "en-us")
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
			"notify-send", "-a", "wepapered", "WePapered", body)
	} else {
		cmd = exec.Command("notify-send", "-a", "wepapered", "WePapered", body)
	}
	if err := cmd.Run(); err != nil {
		log.Printf("[wepapered] notify failed: %v", err)
	}
}
