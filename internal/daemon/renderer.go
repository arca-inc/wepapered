package daemon

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// lweLogFilter wraps an LWE subprocess's stderr and drops the high-volume
// "Fontconfig warning:" lines. CEF bundles an older libfontconfig that doesn't
// understand newer directives (e.g. xsi:nil) in the system fontconfig 2.18.1
// config, so every CEF process re-emits hundreds of identical warnings per web
// wallpaper. They are benign; everything else (including crash output) passes
// straight through. Line-buffered so prefixes can be matched across Write calls.
type lweLogFilter struct {
	w   *os.File
	mu  sync.Mutex
	buf string
}

func newLWELogFilter(w *os.File) *lweLogFilter { return &lweLogFilter{w: w} }

func (f *lweLogFilter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf += string(p)
	for {
		i := strings.IndexByte(f.buf, '\n')
		if i < 0 {
			break
		}
		line := f.buf[:i+1]
		f.buf = f.buf[i+1:]
		if !lweNoiseLine(line) {
			f.w.WriteString(line) //nolint
		}
	}
	return len(p), nil
}

func lweNoiseLine(line string) bool {
	t := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(t, "Fontconfig warning:") || strings.HasPrefix(t, "Fontconfig error:") {
		return true
	}
	// CEF launches its subprocesses as a standalone helper binary rather than
	// forking the zygote, so a child never finds the ICU data file descriptor in
	// its GlobalDescriptors store. Chromium logs this at ERROR level and then
	// falls back to loading icudtl.dat from resources_dir_path (which we set), so
	// the message is benign noise emitted once per web wallpaper subprocess.
	return strings.Contains(t, "icu_util.cc") &&
		strings.Contains(t, "Invalid file descriptor to ICU data received")
}

func init() {
	if os.Getuid() == 0 {
		log.Printf("[renderer] WARNING: running as root — embedded LWE cannot connect to Wayland. Run wepapered as %s.", sessionUsername())
	}
	lweSetSubprocessPath(lwesubprocessbin)
}

// screenProc tracks a single per-screen linux-wallpaperengine subprocess.
type screenProc struct {
	cmd         *exec.Cmd
	doneCh      chan struct{} // closed when subprocess exits
	output      string
	bgDir       string            // currently rendering wallpaper directory
	presetDir   string            // preset/asset directory (may differ for same-framework web presets)
	props       map[string]string // currently applied --set-property overrides
	typ         string            // wallpaper type ("scene", "web", "video", …)
	ctrlSock    string        // unix socket path for IPC hot-swap / stop
	readyCh     chan struct{} // closed when LWE signals READY on the ready pipe
	hotswapping bool          // IPC hot-swap in progress (protected by Renderer.mu)
}

// Renderer runs one linux-wallpaperengine subprocess per monitor.
// Wallpaper changes use IPC hot-swap when possible; only if that fails
// does the process get killed and restarted.
type Renderer struct {
	// applyMu serializes whole Apply/Reload operations so a Reload's Stop+relaunch is
	// atomic with respect to any other Apply (watchdog re-apply, new selection). The
	// finer-grained mu still guards the screens map within an operation.
	applyMu      sync.Mutex
	mu           sync.Mutex
	screens      map[string]*screenProc // keyed by Wayland output name
	cfg          *Config
	loadingShown map[string]hyprOutput // outputs currently showing the loading overlay
	lastState    *DaemonState
	applyTrigger chan struct{}  // closed/replaced to trigger a re-apply
	crashCounts  map[string]int // consecutive rapid-crash count per output
}

func newRenderer(cfg *Config) *Renderer {
	return &Renderer{
		cfg:          cfg,
		screens:      make(map[string]*screenProc),
		loadingShown: make(map[string]hyprOutput),
		applyTrigger: make(chan struct{}, 1),
		crashCounts:  make(map[string]int),
	}
}

// ── IPC helpers ───────────────────────────────────────────────────────────────

// sendCtrlLoadJSON sends a JSON load command to the LWE control socket and blocks
// until LWE responds with "READY".
func sendCtrlLoadJSON(sockPath, bgDir, presetDir string, props map[string]string) error {
	payload := map[string]interface{}{
		"cmd": "load",
		"bg":  bgDir,
	}
	if presetDir != "" {
		payload["preset_dir"] = presetDir
	}
	if len(props) > 0 {
		payload["props"] = props
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	conn, err := net.DialTimeout("unix", sockPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", sockPath, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return err
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(buf[:n])), "READY") {
		return fmt.Errorf("unexpected response: %q", string(buf[:n]))
	}
	return nil
}

// sendCtrlLoad is the legacy plain-text protocol (kept for compatibility).
func sendCtrlLoad(sockPath, bgDir string) error {
	return sendCtrlLoadJSON(sockPath, bgDir, "", nil)
}

// sendCtrlInspect asks an LWE subprocess to serialize its live scene graph and
// returns the raw JSON reply ("{}" when the wallpaper isn't a scene). The reply
// can be large (one entry per scene object), so it's drained until EOF.
func sendCtrlInspect(sockPath string) ([]byte, error) {
	conn, err := net.DialTimeout("unix", sockPath, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", sockPath, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte("inspect\n")); err != nil {
		return nil, err
	}
	return io.ReadAll(conn)
}

// inspectableScreens returns a snapshot of the live screens that own a control
// socket, keyed by Wayland output name. Used by the debug inspector to discover
// which monitors can be introspected.
func (r *Renderer) inspectableScreens() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.screens))
	for name, sp := range r.screens {
		if sp.ctrlSock != "" {
			out[name] = sp.ctrlSock
		}
	}
	return out
}

// propsEqual reports whether two --set-property override maps are equal, so the
// renderer reloads a screen when a wallpaper property changes.
func propsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sendCtrlStop(sockPath string) {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte("stop\n")) //nolint
}

// ── Environment helpers ───────────────────────────────────────────────────────

// waylandEnvOverrides builds a subprocess environment for the session's Wayland
// compositor: the shared core base env, plus any compositor-specific overrides
// (only when our own env lacks them) and the given extras on top.
func waylandEnvOverrides(extra map[string]string) []string {
	overrides := map[string]string{}
	for k, v := range sys.EnvOverrides() {
		if os.Getenv(k) == "" {
			overrides[k] = v
		}
	}
	for k, v := range extra {
		overrides[k] = v
	}
	return waylandSessionEnv(overrides)
}

func lweSubprocEnv() []string {
	extras := map[string]string{
		"LWE_CEF_SUBPROCESS_PATH": lwesubprocessbin,
	}
	// Auto-select the CEF web-wallpaper GPU backend from the system (env overrides
	// still win — see webGPUEnv).
	for k, v := range webGPUEnv() {
		extras[k] = v
	}
	icuShim := filepath.Join(lweOutputDir, "liblwe_cef_icu_fix.so")
	if _, err := os.Stat(icuShim); err == nil {
		extras["LD_PRELOAD"] = icuShim
	}
	// Ensure the output dir is in LD_LIBRARY_PATH so CEF subprocesses can find
	// bundled libraries (libvk_swiftshader.so etc.) regardless of cwd.
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		extras["LD_LIBRARY_PATH"] = lweOutputDir + ":" + existing
	} else {
		extras["LD_LIBRARY_PATH"] = lweOutputDir
	}
	return waylandEnvOverrides(extras)
}

var webGPULogOnce sync.Once

// webGPUEnv auto-detects the environment that selects the CEF web-wallpaper GPU
// backend (web wallpapers render WebGL through ANGLE). It returns env overrides
// consumed by the LWE C++ side (LWE_WEB_ANGLE) and the GLVND EGL loader:
//
//   - a GPU is present (a /dev/dri render node) → "gl-egl" (hardware ANGLE).
//   - NVIDIA's proprietary driver is loaded     → additionally force NVIDIA's EGL
//     vendor, because ANGLE otherwise loads Mesa's libEGL, which can't drive the
//     NVIDIA card and silently falls back to llvmpipe (software).
//   - no GPU                                    → "swiftshader" (software).
//
// Anything the user set explicitly (LWE_WEB_ANGLE, __EGL_VENDOR_LIBRARY_FILENAMES)
// is left untouched so manual overrides win.
func webGPUEnv() map[string]string {
	out := map[string]string{}
	if v := os.Getenv("LWE_WEB_ANGLE"); v != "" {
		webGPULogOnce.Do(func() { log.Printf("[renderer] web GPU backend: %s (LWE_WEB_ANGLE override)", v) })
		return out
	}
	if !hasDRMRenderNode() {
		out["LWE_WEB_ANGLE"] = "swiftshader"
		webGPULogOnce.Do(func() { log.Printf("[renderer] web GPU backend: swiftshader (no GPU detected)") })
		return out
	}
	out["LWE_WEB_ANGLE"] = "gl-egl"
	vendor := ""
	if os.Getenv("__EGL_VENDOR_LIBRARY_FILENAMES") == "" {
		if v := nvidiaEGLVendorJSON(); v != "" {
			out["__EGL_VENDOR_LIBRARY_FILENAMES"] = v
			vendor = v
		}
	}
	webGPULogOnce.Do(func() {
		if vendor != "" {
			log.Printf("[renderer] web GPU backend: gl-egl, hardware (NVIDIA EGL %s)", vendor)
		} else {
			log.Printf("[renderer] web GPU backend: gl-egl, hardware")
		}
	})
	return out
}

// hasDRMRenderNode reports whether the system exposes a GPU render node.
func hasDRMRenderNode() bool {
	m, _ := filepath.Glob("/dev/dri/renderD*")
	return len(m) > 0
}

// nvidiaEGLVendorJSON returns the path to NVIDIA's GLVND EGL vendor ICD config if
// the proprietary NVIDIA kernel driver is loaded and its EGL vendor file exists,
// otherwise "". Used to force ANGLE onto NVIDIA's libEGL instead of Mesa's.
func nvidiaEGLVendorJSON() string {
	if _, err := os.Stat("/sys/module/nvidia"); err != nil {
		return ""
	}
	for _, dir := range []string{"/usr/share/glvnd/egl_vendor.d", "/etc/glvnd/egl_vendor.d"} {
		if m, _ := filepath.Glob(filepath.Join(dir, "*nvidia*.json")); len(m) > 0 {
			sort.Strings(m)
			return m[0]
		}
	}
	return ""
}

// ── Loading overlay ───────────────────────────────────────────────────────────

const (
	// loadingOverlayDelay is held both after showing the overlay (so it maps before the
	// old wallpaper is killed) and after the new wallpaper's first paint (CEF's first
	// frame can be blank) before hiding it.
	loadingOverlayDelay = 300 * time.Millisecond
	// readyTimeout caps how long a swap waits for the new wallpaper's first frame.
	readyTimeout = 10 * time.Second
)

type loadingOutputEntry struct {
	output      hyprOutput
	previewPath string
}

func (r *Renderer) startLoadingBgWithPreviewLocked(entries []loadingOutputEntry) {
	// The loading screen is the native overlay (logo + animated bar). Caller holds r.mu.
	r.startLoadingOverlayLocked(entries)
}

// startLoadingOverlayLocked shows the persistent loading overlay on each output.
// Must be called with r.mu held.
func (r *Renderer) startLoadingOverlayLocked(entries []loadingOutputEntry) {
	for _, e := range entries {
		r.spawnLoadingOverlayLocked(e.output)
	}
}

// spawnLoadingOverlayLocked shows the persistent loading overlay on one output (no-op
// if already shown). Just toggles the pre-created GTK window — instant. Must hold r.mu.
func (r *Renderer) spawnLoadingOverlayLocked(o hyprOutput) {
	if _, ok := r.loadingShown[o.Name]; ok {
		return
	}
	overlayShow(o.Name, o.X, o.Y)
	r.loadingShown[o.Name] = o
	log.Printf("[renderer] loading overlay on %s", o.Name)
}

// stopLoadingOverlayLocked hides the loading overlay for one output. Must hold r.mu.
func (r *Renderer) stopLoadingOverlayLocked(name string) {
	if _, ok := r.loadingShown[name]; !ok {
		return
	}
	overlayHide(name)
	delete(r.loadingShown, name)
}

func (r *Renderer) stopLoadingBgLocked() {
	// Hide all loading overlays.
	for name := range r.loadingShown {
		r.stopLoadingOverlayLocked(name)
	}
}

// ── Apply (diff-based with IPC hot-swap) ─────────────────────────────────────

// SetConfig swaps the renderer's config pointer (guarded by mu, which the launch path
// reads under). Used by daemon reload so subsequently launched subprocesses pick up new
// settings (audio device, preferred player, …) which are passed via env at launch time.
func (r *Renderer) SetConfig(cfg *Config) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
}

func (r *Renderer) Apply(state *DaemonState) {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	r.applyLocked(state)
}

func (r *Renderer) applyLocked(state *DaemonState) {
	r.mu.Lock()
	r.lastState = state
	defer r.mu.Unlock()

	if len(state.Monitors) == 0 {
		r.stopAllLocked()
		return
	}

	outputs, err := sys.Outputs()
	if err != nil {
		log.Printf("[renderer] %s: failed to enumerate outputs: %v", sys.Name(), err)
		return
	}
	if len(outputs) == 0 {
		log.Printf("[renderer] %s: no outputs", sys.Name())
		return
	}

	assetsDir := filepath.Join(r.cfg.WEPath, "assets")

	type wantEntry struct {
		bgDir       string
		presetDir   string
		props       map[string]string
		previewPath string
		label       string
		title       string
		typ         string
	}
	desired := map[string]wantEntry{}
	for label, mw := range state.Monitors {
		loc := -1
		fmt.Sscanf(label, "Monitor%d", &loc)
		if loc < 0 || loc >= len(outputs) {
			log.Printf("[renderer] no Wayland output for %s (loc=%d, have %d)", label, loc, len(outputs))
			continue
		}
		outName := outputs[loc].Name

		var bgDir string
		if !isLWESupportedType(mw.Type) {
			bgDir = errorWallpaperDir(label, mw.Title, mw.Type)
			if bgDir == "" {
				log.Printf("[renderer] skipping %s (%q): unsupported type %q", label, mw.Title, mw.Type)
				continue
			}
		} else {
			p := mw.LinuxPath
			if mw.RenderDir != "" {
				p = mw.RenderDir
			}
			bgDir = bgDirFromLinuxPath(p)
			if bgDir == "" {
				log.Printf("[renderer] cannot resolve bg dir for %s", mw.LinuxPath)
				continue
			}
		}

		// Resolve preview image path for the loading state
		previewPath := ""
		if mw.PreviewFile != "" {
			dir := mw.LinuxPath
			if !isDir(dir) {
				dir = filepath.Dir(dir)
			}
			candidate := filepath.Join(dir, mw.PreviewFile)
			if _, err := os.Stat(candidate); err == nil {
				previewPath = candidate
			}
		}

		desired[outName] = wantEntry{bgDir, mw.PresetDir, mw.Props, previewPath, label, mw.Title, mw.Type}
	}

	type hwWork struct {
		sp          *screenProc
		newBg       string
		presetDir   string
		props       map[string]string
		previewPath string
		output      string
		newTyp      string
	}
	var toStop []*screenProc
	var toHotswap []hwWork

	for outName, sp := range r.screens {
		w, keep := desired[outName]
		if !keep {
			toStop = append(toStop, sp)
			delete(r.screens, outName)
			if sp.cmd.Process != nil {
				sp.cmd.Process.Signal(syscall.SIGTERM)
			}
		} else if w.bgDir != sp.bgDir || w.presetDir != sp.presetDir || !propsEqual(w.props, sp.props) {
			if sp.ctrlSock != "" && !sp.hotswapping {
				toHotswap = append(toHotswap, hwWork{sp, w.bgDir, w.presetDir, w.props, w.previewPath, outName, w.typ})
				sp.hotswapping = true
			} else if !sp.hotswapping {
				// No ctrl socket yet or process died: kill and restart.
				toStop = append(toStop, sp)
				delete(r.screens, outName)
				if sp.cmd.Process != nil {
					sp.cmd.Process.Signal(syscall.SIGTERM)
				}
			}
			// if sp.hotswapping: already in flight, skip — next Apply() will catch up
		}
	}

	// New screens: desired but not currently running
	var startingOutputs []hyprOutput
	for outName := range desired {
		if _, running := r.screens[outName]; !running {
			for _, o := range outputs {
				if o.Name == outName {
					startingOutputs = append(startingOutputs, o)
					break
				}
			}
		}
	}

	// Loading bg only for genuinely new screens — NOT for IPC hot-swaps.
	// During a hot-swap, LWE keeps the old wallpaper's last frame on the
	// Wayland surface, so a black placeholder only makes the transition worse.
	var loadingEntries []loadingOutputEntry
	for _, o := range startingOutputs {
		preview := ""
		if w, ok := desired[o.Name]; ok {
			preview = w.previewPath
		}
		loadingEntries = append(loadingEntries, loadingOutputEntry{o, preview})
	}

	if len(toStop) == 0 && len(startingOutputs) == 0 && len(toHotswap) == 0 {
		return
	}

	// Always clean up any stale loading placeholder first.
	r.stopLoadingBgLocked()
	if len(loadingEntries) > 0 {
		r.startLoadingBgWithPreviewLocked(loadingEntries)
	}

	// Wait for stopped screens to exit (release lock while waiting)
	if len(toStop) > 0 {
		r.mu.Unlock()
		for _, sp := range toStop {
			<-sp.doneCh
		}
		r.mu.Lock()
	}

	// Collect ready channels from all screens that will start rendering
	var allReadyChs []<-chan struct{}

	// Launch new / restarted screens
	for outName, w := range desired {
		if _, running := r.screens[outName]; running {
			continue
		}
		log.Printf("[renderer] %s → %s : %s (%s)", w.label, outName, w.title, w.typ)
		sp := r.launchScreenLocked(outName, w.bgDir, w.presetDir, w.props, assetsDir)
		if sp != nil {
			sp.typ = w.typ
			r.screens[outName] = sp
			allReadyChs = append(allReadyChs, sp.readyCh)
		}
	}

	// Hot-swap goroutines. Preferred path is a TRUE in-place swap over the ctrl socket:
	// the LWE process stays alive, tears down the current wallpaper and recreates the new
	// one internally, replying READY on its first frame — no process kill, no Go-side
	// relaunch, and two CEF instances never coexist. Only if that fails do we fall back
	// to killing the process and relaunching. The loading overlay covers either gap.
	capturedAssetsDir := assetsDir
	for _, hw := range toHotswap {
		hwDoneCh := make(chan struct{})
		allReadyChs = append(allReadyChs, hwDoneCh)

		hwSp, hwNewBg, hwPresetDir, hwProps, hwOut, hwOldTyp, hwNewTyp := hw.sp, hw.newBg, hw.presetDir, hw.props, hw.output, hw.sp.typ, hw.newTyp

		go func() {
			defer close(hwDoneCh)
			log.Printf("[renderer] hot-swap %s → %s", hwOut, hwNewBg)

			hideLoading := func() {
				r.mu.Lock()
				r.stopLoadingOverlayLocked(hwOut)
				r.mu.Unlock()
			}

			// Show the overlay and hold so it maps before the wallpaper changes (covers
			// the teardown→recreate / relaunch gap).
			r.mu.Lock()
			for _, o := range outputs {
				if o.Name == hwOut {
					r.spawnLoadingOverlayLocked(o)
					break
				}
			}
			r.mu.Unlock()
			time.Sleep(loadingOverlayDelay)

			// In-place swap (process stays alive) only works between non-web wallpapers.
			// Re-initialising CEF in-process segfaults, so any web wallpaper (old or new)
			// goes straight to a clean relaunch in a fresh process.
			oldIsWeb := strings.EqualFold(hwOldTyp, "web")
			newIsWeb := strings.EqualFold(hwNewTyp, "web")

			inPlaceOK := false
			if !oldIsWeb && !newIsWeb {
				// 1) In-place swap. Blocks until the new wallpaper's first frame (READY).
				if err := sendCtrlLoadJSON(hwSp.ctrlSock, hwNewBg, hwPresetDir, hwProps); err == nil {
					r.mu.Lock()
					hwSp.bgDir = hwNewBg
					hwSp.presetDir = hwPresetDir
					hwSp.props = hwProps
					hwSp.typ = hwNewTyp
					hwSp.hotswapping = false
					r.mu.Unlock()
					inPlaceOK = true
				} else {
					log.Printf("[renderer] hot-swap %s: ctrl load failed (%v); relaunching", hwOut, err)
				}
			}

			if !inPlaceOK {
				// 2) Relaunch: kill the old process and start a fresh one (the path web
				// swaps always take, and the fallback if an in-place load fails).
				if hwSp.cmd.Process != nil {
					hwSp.cmd.Process.Signal(syscall.SIGTERM)
				}
				<-hwSp.doneCh
				r.mu.Lock()
				delete(r.screens, hwOut)
				hwSp.hotswapping = false
				newSp := r.launchScreenLocked(hwOut, hwNewBg, hwPresetDir, hwProps, capturedAssetsDir)
				if newSp != nil {
					newSp.typ = hwNewTyp
					r.screens[hwOut] = newSp
				}
				r.mu.Unlock()
				if newSp == nil {
					hideLoading()
					return
				}
				select {
				case <-newSp.readyCh:
				case <-time.After(readyTimeout):
					log.Printf("[renderer] hot-swap %s: READY timeout", hwOut)
				}
			}

			// Hold past first paint (CEF's first frame can be blank), then hide.
			time.Sleep(loadingOverlayDelay)
			hideLoading()
			log.Printf("[renderer] hot-swap %s: complete", hwOut)
		}()
	}

	// Hide loading bg when all screens are ready, or after timeout
	if len(loadingEntries) > 0 {
		chs := allReadyChs
		go func() {
			timer := time.NewTimer(8 * time.Second)
			defer timer.Stop()
			for _, ch := range chs {
				select {
				case <-ch:
				case <-timer.C:
					log.Printf("[renderer] ready timeout — hiding loading bg")
					r.mu.Lock()
					r.stopLoadingBgLocked()
					r.mu.Unlock()
					return
				}
			}
			// Hold the overlay past first paint so CEF has fully rendered.
			time.Sleep(loadingOverlayDelay)
			r.mu.Lock()
			r.stopLoadingBgLocked()
			r.mu.Unlock()
		}()
	}
}

// launchScreenLocked starts an LWE subprocess for one output.
// Sets up a ready-pipe (fd 3 in subprocess) and a ctrl socket path.
// Must be called with r.mu held.
func (r *Renderer) launchScreenLocked(outputName, bgDir, presetDir string, props map[string]string, assetsDir string) *screenProc {
	// Reset crash counter — explicit (re)launch starts a fresh slate.
	r.crashCounts[outputName] = 0

	// Create pipe: parent reads, subprocess writes "READY\n" when rendering starts.
	readR, readW, pipeErr := os.Pipe()
	if pipeErr != nil {
		log.Printf("[renderer] pipe for %s: %v", outputName, pipeErr)
	}

	ctrlSockPath := "/tmp/wepapered-ctrl-" + outputName + ".sock"
	os.Remove(ctrlSockPath)

	readyCh := make(chan struct{})

	env := append(lweSubprocEnv(), "WEPAPERED_CTRL_SOCK="+ctrlSockPath)
	// Forced audio capture device (empty = LWE follows the default output monitor).
	if dev := strings.TrimSpace(r.cfg.AudioDevice); dev != "" {
		env = append(env, "LWE_AUDIO_DEVICE="+dev)
	}
	// Forward now-playing track into web wallpapers' header/subheader text.
	if r.cfg.NowPlayingText {
		env = append(env, "LWE_MEDIA_TO_TEXT=1")
	}
	// Media source: when the album-art proxy runs, read it (it mirrors the active
	// player and adds resolved cover art); otherwise use the user's preferred
	// player list directly.
	if r.cfg.AlbumArtEnabled() {
		env = append(env, "LWE_MEDIA_PLAYER="+mediaProxyIdentity)
	} else if mp := strings.TrimSpace(r.cfg.MediaPlayer); mp != "" {
		env = append(env, "LWE_MEDIA_PLAYER="+mp)
	}

	cmd := exec.Command(
		lwebin,
		"--assets-dir", assetsDir,
		"--silent",
		"--fps", strconv.Itoa(func() int {
			if r.cfg.FPS > 0 {
				return r.cfg.FPS
			}
			return 30
		}()),
		// No --no-audio-processing: let LWE enable system-audio capture, which it
		// already gates to wallpapers declaring supportsaudioprocessing (audio
		// visualizers). Drives wallpaperRegisterAudioListener on web + the scene
		// audio spectrum; non-audio wallpapers pay no capture cost.
		"--screen-root", outputName,
		"--bg", bgDir,
	)
	if presetDir != "" {
		cmd.Args = append(cmd.Args, "--preset-dir", presetDir)
	}
	for k, v := range props {
		cmd.Args = append(cmd.Args, "--set-property", k+"="+v)
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = newLWELogFilter(os.Stderr)

	if readW != nil {
		// Pass write-end as fd 3 in the subprocess
		cmd.ExtraFiles = []*os.File{readW}
		cmd.Env = append(cmd.Env, "WEPAPERED_READY_FD=3")
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[renderer] start lwe for %s: %v", outputName, err)
		if readR != nil {
			readR.Close()
		}
		if readW != nil {
			readW.Close()
		}
		return nil
	}

	// Parent closes its copy of the write-end; subprocess owns it now.
	if readW != nil {
		readW.Close()
	}

	// Watch ready pipe: close readyCh when LWE writes READY or exits.
	if readR != nil {
		go func() {
			defer readR.Close()
			defer close(readyCh)
			buf := make([]byte, 32)
			readR.Read(buf) //nolint — any read (or EOF on LWE exit) is sufficient
		}()
	} else {
		close(readyCh)
	}

	doneCh := make(chan struct{})
	sp := &screenProc{
		cmd:       cmd,
		doneCh:    doneCh,
		output:    outputName,
		bgDir:     bgDir,
		presetDir: presetDir,
		props:     props,
		ctrlSock:  ctrlSockPath,
		readyCh:   readyCh,
	}
	// typ is set by Apply() after creation
	startTime := time.Now()
	go func() {
		defer close(doneCh)
		if err := cmd.Wait(); err != nil {
			log.Printf("[renderer] lwe %s (pid=%d) exited: %v", outputName, cmd.Process.Pid, err)
		} else {
			log.Printf("[renderer] lwe %s (pid=%d) exited", outputName, cmd.Process.Pid)
		}
		// Watchdog: if this process is still the registered screen, remove it and
		// schedule a re-apply with backoff.
		r.mu.Lock()
		if r.screens[outputName] != sp {
			r.mu.Unlock()
			return
		}
		if sp.hotswapping {
			// Intentional kill as part of a hot-swap (sequential web→X path): the
			// hot-swap goroutine owns deleting this from the map and launching the
			// replacement. Do NOT treat it as a crash or schedule a watchdog re-apply
			// — that spawns a duplicate process for the output.
			r.mu.Unlock()
			return
		}
		delete(r.screens, outputName)
		uptime := time.Since(startTime)
		if uptime > 30*time.Second {
			r.crashCounts[outputName] = 0
		}
		r.crashCounts[outputName]++
		count := r.crashCounts[outputName]
		r.mu.Unlock()

		const maxCrashes = 5
		if count > maxCrashes {
			log.Printf("[renderer] %s: crashed %d times rapidly, giving up restart", outputName, count)
			return
		}
		delay := time.Duration(count) * 2 * time.Second
		log.Printf("[renderer] %s: crash #%d — retrying in %v", outputName, count, delay)
		time.Sleep(delay)
		select {
		case r.applyTrigger <- struct{}{}:
		default:
		}
	}()

	log.Printf("[renderer] started lwe pid=%d for %s", cmd.Process.Pid, outputName)
	return sp
}

// killStrayRenderers terminates leftover linux-wallpaperengine background
// renderers from a previous daemon instance that exited uncleanly (crash,
// kill -9, or a shutdown where tray.Run() never returned to run Stop()). The
// subprocesses aren't in our process group and carry no Pdeathsig, so they
// otherwise survive and fight the new instance over the same Wayland output
// (duplicate --screen-root processes → flicker and crash backoff). Called once
// on daemon startup, before we launch our own renderers. Targets only the
// per-output bg renderers, never the hosted UI window (--ui-window).
func killStrayRenderers() {
	if err := exec.Command("pkill", "-TERM", "-f", "linux-wallpaperengine.*--screen-root").Run(); err == nil {
		log.Printf("[renderer] cleaned up stray renderers from a previous instance")
	}
}

// Stop kills all running renderers and loading placeholders.
// Reload tears down every running screen and relaunches from scratch. A plain Apply()
// hot-swaps in place and would keep the OLD subprocess environment; several settings
// (LWE_AUDIO_DEVICE, LWE_MEDIA_PLAYER, LWE_MEDIA_TO_TEXT) are passed via env at launch,
// so picking them up requires actually restarting the linux-wallpaperengine processes.
func (r *Renderer) Reload(state *DaemonState) {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	r.Stop() // takes mu (not applyMu) — safe to call while holding applyMu
	if state != nil && len(state.Monitors) > 0 {
		r.applyLocked(state)
	}
}

func (r *Renderer) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopAllLocked()
	r.stopLoadingBgLocked()
}

// stopAllLocked signals all screen subprocesses and waits for them.
// Must be called with r.mu held; releases and re-acquires the lock while waiting.
func (r *Renderer) stopAllLocked() {
	if len(r.screens) == 0 {
		return
	}
	all := make([]*screenProc, 0, len(r.screens))
	for _, sp := range r.screens {
		all = append(all, sp)
		if sp.ctrlSock != "" {
			go sendCtrlStop(sp.ctrlSock)
		}
		if sp.cmd.Process != nil {
			sp.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	r.screens = make(map[string]*screenProc)
	r.mu.Unlock()
	for _, sp := range all {
		<-sp.doneCh
	}
	r.mu.Lock()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func bgDirFromLinuxPath(p string) string {
	if p == "" {
		return ""
	}
	info, err := os.Stat(p)
	if err == nil && info.IsDir() {
		return p
	}
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}

func errorWallpaperDir(label, title, typ string) string {
	dir := filepath.Join(os.TempDir(), "wepapered-error", label)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	proj := `{"type":"web","title":"Error","file":"index.html"}`
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(proj), 0o644); err != nil {
		return ""
	}
	safeTitle := html.EscapeString(title)
	safeType := html.EscapeString(typ)
	if safeType == "" {
		safeType = "(unknown)"
	}
	page := fmt.Sprintf(`<!DOCTYPE html>
<html><body style="background:#000;margin:0;display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh">
<div style="color:#cc0000;font:bold 32px monospace">Unsupported wallpaper</div>
<div style="color:#ff4444;font:22px monospace;margin-top:16px">%s</div>
<div style="color:#888888;font:16px monospace;margin-top:10px">type: %s</div>
</body></html>`, safeTitle, safeType)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644); err != nil {
		return ""
	}
	return dir
}

func isLWESupportedType(t string) bool {
	switch strings.ToLower(t) {
	case "scene", "video", "web", "image":
		return true
	}
	return false
}

// Compositor-specific helpers (hyprctl, instance signature, socket dir) and the
// generic Wayland-session helpers (sessionUID/Username/Home) now live in
// internal/compositor and internal/core respectively.
