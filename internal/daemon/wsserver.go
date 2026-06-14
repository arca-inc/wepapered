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

	favMu sync.Mutex // serializes favorite read-modify-write of the config

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
	go s.renderer.Reload(s.state.snapshot())

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

	// Snapshot the state as the rollback baseline for this browse session (Cancel).
	s.sessionBaseline = s.state.snapshot()

	// Send current state immediately so the inject script can restore active wallpapers.
	if stateData, err := json.Marshal(map[string]interface{}{
		"type":  "state",
		"state": s.state,
	}); err == nil {
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
		log.Printf("[WE] updateProfile intercepted — callback will be wrapped")
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
	s.state.Layout = int(v)
	log.Printf("[WE] layout → %d", s.state.Layout)
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}

	// Re-clone the current wallpaper onto every display right away so toggling to
	// clone mode takes effect without re-picking.
	if s.state.Layout == layoutClone {
		if src := s.anyMonitorWallpaper(); src != nil {
			s.cloneToAllOutputs(src)
			saveState(s.state) //nolint
			writeWESelectedWallpapers(s.cfg.Load().WEPath, s.state.Monitors, s.monitorInfos) //nolint
			go s.renderer.Apply(s.state)
		}
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

// openConfigWindow launches the wepapered GTK configuration window (--config).
// Triggered by the WE UI's Settings button (showSettingsDialog). GTK3 runs under
// Wayland, so this can spawn from the daemon process directly.
func (s *WSServer) openConfigWindow() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("[wepapered] cannot find executable for config window: %v", err)
		return
	}
	cmd := exec.Command(exe, "--config")
	if err := cmd.Start(); err != nil {
		log.Printf("[wepapered] failed to open config window: %v", err)
		return
	}
	go cmd.Wait()
	log.Printf("[WE] showSettingsDialog → opened wepapered config")
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
	s.state.BrowserSettings = raw
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	log.Printf("[WE] browser settings persisted (%d bytes)", len(raw))
}

// onAcceptAndClose commits the current selections (OK button): persist state and
// re-write WE's config, and adopt the current state as the new rollback baseline.
func (s *WSServer) onAcceptAndClose() {
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	if err := writeWESelectedWallpapers(s.cfg.Load().WEPath, s.state.Monitors, s.monitorInfos); err != nil {
		log.Printf("[wepapered] WE config write error: %v", err)
	}
	s.sessionBaseline = s.state.snapshot()
	log.Printf("[WE] acceptAndClose — settings saved")
}

// onCancelAndClose rolls back to the baseline captured when the browse UI opened
// (Cancel button), re-rendering the original wallpapers. Best-effort: if no
// baseline was captured, nothing changes.
func (s *WSServer) onCancelAndClose() {
	if s.sessionBaseline == nil {
		log.Printf("[WE] cancelAndClose — no baseline, nothing to roll back")
		return
	}
	restored := s.sessionBaseline.snapshot() // independent copy; keep baseline intact
	s.state.Monitors = restored.Monitors
	s.state.Layout = restored.Layout
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}
	if err := writeWESelectedWallpapers(s.cfg.Load().WEPath, s.state.Monitors, s.monitorInfos); err != nil {
		log.Printf("[wepapered] WE config write error: %v", err)
	}
	log.Printf("[WE] cancelAndClose — rolled back to baseline")
	go s.renderer.Apply(s.state)
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
		linuxPath = winToLinux(winPath, s.cfg.Load().WEPath)
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
		depDir := filepath.Join(filepath.Dir(dir), string(meta.Dependency))
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

	// In clone mode the same wallpaper goes on every display; otherwise just the
	// selected one.
	if s.state.Layout == layoutClone {
		s.cloneToAllOutputs(mw)
	} else {
		s.state.Monitors[monitor] = mw
	}
	if err := saveState(s.state); err != nil {
		log.Printf("[wepapered] state save error: %v", err)
	}

	// Write back into WE's config.json so it remembers on next startup.
	if err := writeWESelectedWallpapers(s.cfg.Load().WEPath, s.state.Monitors, s.monitorInfos); err != nil {
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
