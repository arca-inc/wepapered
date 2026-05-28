package main

import (
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

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
	bgDir       string       // currently rendering wallpaper directory
	ctrlSock    string       // unix socket path for IPC hot-swap / stop
	readyCh     chan struct{} // closed when LWE signals READY on the ready pipe
	hotswapping bool         // IPC hot-swap in progress (protected by Renderer.mu)
}

// Renderer runs one linux-wallpaperengine subprocess per monitor.
// Wallpaper changes use IPC hot-swap when possible; only if that fails
// does the process get killed and restarted.
type Renderer struct {
	mu           sync.Mutex
	screens      map[string]*screenProc // keyed by Wayland output name
	cfg          *Config
	loadingProcs []*exec.Cmd // hyprpaper processes used for loading placeholders
	lastState    *DaemonState
	applyTrigger chan struct{} // closed/replaced to trigger a re-apply
	crashCounts  map[string]int // consecutive rapid-crash count per output
}

func newRenderer(cfg *Config) *Renderer {
	return &Renderer{
		cfg:          cfg,
		screens:      make(map[string]*screenProc),
		applyTrigger: make(chan struct{}, 1),
		crashCounts:  make(map[string]int),
	}
}

const loadingPNGPath = "/tmp/wepapered-loading.png"

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

func waylandEnvOverrides(extra map[string]string) []string {
	overrides := map[string]string{"XDG_SESSION_TYPE": "wayland"}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		overrides["WAYLAND_DISPLAY"] = "wayland-1"
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		overrides["XDG_RUNTIME_DIR"] = fmt.Sprintf("/run/user/%d", sessionUID())
	}
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		if sig := hyprlandInstanceSig(); sig != "" {
			overrides["HYPRLAND_INSTANCE_SIGNATURE"] = sig
		}
	}
	for k, v := range extra {
		overrides[k] = v
	}
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			if _, skip := overrides[kv[:idx]]; skip {
				continue
			}
		}
		result = append(result, kv)
	}
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

func lweSubprocEnv() []string {
	extras := map[string]string{
		"LWE_CEF_SUBPROCESS_PATH": lwesubprocessbin,
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

// ── hyprctl helpers ───────────────────────────────────────────────────────────

func hyprctlOutput(args ...string) ([]byte, error) {
	baseEnv := waylandEnvOverrides(nil)

	seen := map[string]bool{}
	var sigs []string
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
		sigs = append(sigs, sig)
		seen[sig] = true
	}
	if entries, err := os.ReadDir(hyprDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				sigs = append(sigs, e.Name())
				seen[e.Name()] = true
			}
		}
	}
	if len(sigs) == 0 {
		return nil, fmt.Errorf("no Hyprland instances found")
	}
	var lastErr error
	for _, sig := range sigs {
		cmd := exec.Command("hyprctl", append([]string{"-i", sig}, args...)...)
		cmd.Env = append(append([]string(nil), baseEnv...), "HYPRLAND_INSTANCE_SIGNATURE="+sig)
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// ── Loading background ────────────────────────────────────────────────────────

func ensureLoadingPNG() error {
	if _, err := os.Stat(loadingPNGPath); err == nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	c := color.RGBA{10, 10, 15, 255}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(loadingPNGPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

type loadingOutputEntry struct {
	output      hyprOutput
	previewPath string
}

func (r *Renderer) startLoadingBgLocked(outputs []hyprOutput) {
	entries := make([]loadingOutputEntry, len(outputs))
	for i, o := range outputs {
		entries[i] = loadingOutputEntry{o, ""}
	}
	r.startLoadingBgWithPreviewLocked(entries)
}

func (r *Renderer) startLoadingBgWithPreviewLocked(entries []loadingOutputEntry) {
	if err := ensureLoadingPNG(); err != nil {
		log.Printf("[renderer] loading PNG: %v", err)
		return
	}

	var cfg strings.Builder
	cfg.WriteString("splash = false\n")
	cfg.WriteString("preload = " + loadingPNGPath + "\n")

	// Preload any preview images that exist on disk
	preloaded := map[string]bool{loadingPNGPath: true}
	for _, e := range entries {
		if e.previewPath != "" && !preloaded[e.previewPath] {
			if _, err := os.Stat(e.previewPath); err == nil {
				cfg.WriteString("preload = " + e.previewPath + "\n")
				preloaded[e.previewPath] = true
			}
		}
	}

	for _, e := range entries {
		img := loadingPNGPath
		if e.previewPath != "" && preloaded[e.previewPath] {
			img = e.previewPath
		}
		cfg.WriteString("wallpaper = " + e.output.Name + "," + img + "\n")
	}

	cfgPath := "/tmp/wepapered-hyprpaper.conf"
	if err := os.WriteFile(cfgPath, []byte(cfg.String()), 0644); err != nil {
		log.Printf("[renderer] hyprpaper config: %v", err)
		return
	}
	cmd := exec.Command("hyprpaper", "--config", cfgPath)
	cmd.Env = waylandEnvOverrides(nil)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[renderer] hyprpaper start: %v", err)
		return
	}
	log.Printf("[renderer] loading placeholder pid=%d on %d output(s)", cmd.Process.Pid, len(entries))
	r.loadingProcs = append(r.loadingProcs, cmd)
}

func (r *Renderer) stopLoadingBgLocked() {
	for _, cmd := range r.loadingProcs {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}
	r.loadingProcs = nil
}

// ── Apply (diff-based with IPC hot-swap) ─────────────────────────────────────

func (r *Renderer) Apply(state *DaemonState) {
	r.mu.Lock()
	r.lastState = state
	defer r.mu.Unlock()

	if len(state.Monitors) == 0 {
		r.stopAllLocked()
		return
	}

	outputs, err := hyprlandOutputs()
	if err != nil {
		log.Printf("[renderer] hyprctl failed: %v", err)
		return
	}
	if len(outputs) == 0 {
		log.Printf("[renderer] no outputs from hyprctl")
		return
	}

	assetsDir := filepath.Join(r.cfg.WEPath, "assets")

	type wantEntry struct {
		bgDir     string
		presetDir string
		props     map[string]string
		previewPath string
		label     string
		title     string
		typ       string
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
		sp        *screenProc
		newBg     string
		presetDir string
		props     map[string]string
		previewPath string
		output    string
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
		} else if w.bgDir != sp.bgDir {
			if sp.ctrlSock != "" && !sp.hotswapping {
				toHotswap = append(toHotswap, hwWork{sp, w.bgDir, w.presetDir, w.props, w.previewPath, outName})
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

	// Loading bg covers: new starts + hot-swapping screens.
	// Use the incoming wallpaper's preview image when available.
	var loadingEntries []loadingOutputEntry
	for _, o := range startingOutputs {
		preview := ""
		if w, ok := desired[o.Name]; ok {
			preview = w.previewPath
		}
		loadingEntries = append(loadingEntries, loadingOutputEntry{o, preview})
	}
	for _, hw := range toHotswap {
		for _, o := range outputs {
			if o.Name == hw.output {
				loadingEntries = append(loadingEntries, loadingOutputEntry{o, hw.previewPath})
				break
			}
		}
	}

	if len(toStop) == 0 && len(startingOutputs) == 0 && len(toHotswap) == 0 {
		return
	}

	if len(loadingEntries) > 0 {
		r.stopLoadingBgLocked()
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
			r.screens[outName] = sp
			allReadyChs = append(allReadyChs, sp.readyCh)
		}
	}

	// Initiate IPC hot-swaps in background goroutines
	capturedAssetsDir := assetsDir
	for _, hw := range toHotswap {
		hwDoneCh := make(chan struct{})
		allReadyChs = append(allReadyChs, hwDoneCh)

		hwSp, hwNewBg, hwPresetDir, hwProps, hwOut := hw.sp, hw.newBg, hw.presetDir, hw.props, hw.output

		go func() {
			defer close(hwDoneCh)
			log.Printf("[renderer] hot-swap %s → %s", hwOut, hwNewBg)

			if err := sendCtrlLoadJSON(hwSp.ctrlSock, hwNewBg, hwPresetDir, hwProps); err != nil {
				log.Printf("[renderer] hot-swap %s failed (%v), falling back to restart", hwOut, err)
				if hwSp.cmd.Process != nil {
					hwSp.cmd.Process.Signal(syscall.SIGTERM)
				}
				<-hwSp.doneCh

				r.mu.Lock()
				delete(r.screens, hwOut)
				hwSp.hotswapping = false
				newSp := r.launchScreenLocked(hwOut, hwNewBg, hwPresetDir, hwProps, capturedAssetsDir)
				if newSp != nil {
					r.screens[hwOut] = newSp
				}
				r.mu.Unlock()
				return
			}

			r.mu.Lock()
			hwSp.bgDir = hwNewBg
			hwSp.hotswapping = false
			r.mu.Unlock()
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

	cmd := exec.Command(lwebin,
		"--assets-dir", assetsDir,
		"--silent",
		"--fps", "30",
		"--no-audio-processing",
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
	cmd.Stderr = os.Stderr

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
		cmd:      cmd,
		doneCh:   doneCh,
		output:   outputName,
		bgDir:    bgDir,
		ctrlSock: ctrlSockPath,
		readyCh:  readyCh,
	}
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

// Stop kills all running renderers and loading placeholders.
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

type hyprOutput struct {
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

func hyprlandOutputs() ([]hyprOutput, error) {
	var out []byte
	var err error
	if os.Getuid() == 0 {
		sig := hyprlandInstanceSig()
		waylandEnv := []string{
			"HOME=" + sessionHome(),
			"WAYLAND_DISPLAY=wayland-1",
			fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", sessionUID()),
			"XDG_SESSION_TYPE=wayland",
			"HYPRLAND_INSTANCE_SIGNATURE=" + sig,
		}
		cmd := exec.Command("sudo", append([]string{"-u", sessionUsername(), "env"}, append(waylandEnv, "hyprctl", "monitors", "-j")...)...)
		out, err = cmd.Output()
	} else {
		out, err = hyprctlOutput("monitors", "-j")
	}
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name string `json:"name"`
		X    int    `json:"x"`
		Y    int    `json:"y"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	result := make([]hyprOutput, 0, len(raw))
	for _, m := range raw {
		result = append(result, hyprOutput{Name: m.Name, X: m.X, Y: m.Y})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].X != result[j].X {
			return result[i].X < result[j].X
		}
		return result[i].Y < result[j].Y
	})
	return result, nil
}

func errorWallpaperDir(label, title, typ string) string {
	dir := filepath.Join(os.TempDir(), "wepapered-error", label)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ""
	}
	proj := `{"type":"web","title":"Error","file":"index.html"}`
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(proj), 0644); err != nil {
		return ""
	}
	safeTitle := html.EscapeString(title)
	safeType := html.EscapeString(typ)
	if safeType == "" {
		safeType = "(unknown)"
	}
	page := fmt.Sprintf(`<!DOCTYPE html>
<html><body style="background:#000;margin:0;display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh">
<div style="color:#cc0000;font:bold 32px monospace">Wallpaper non supporté</div>
<div style="color:#ff4444;font:22px monospace;margin-top:16px">%s</div>
<div style="color:#888888;font:16px monospace;margin-top:10px">type: %s</div>
</body></html>`, safeTitle, safeType)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0644); err != nil {
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

func hyprlandInstanceSig() string {
	entries, _ := os.ReadDir(hyprDir())
	for _, e := range entries {
		if e.IsDir() {
			return e.Name()
		}
	}
	return ""
}

// ── Session user helpers ───────────────────────────────────────────────────────

// sessionUID returns the UID of the Wayland session owner.
// When wepapered runs as root (e.g. via sudo), SUDO_UID gives the real user.
func sessionUID() int {
	if os.Getuid() == 0 {
		if s := os.Getenv("SUDO_UID"); s != "" {
			var uid int
			fmt.Sscan(s, &uid)
			if uid > 0 {
				return uid
			}
		}
	}
	return os.Getuid()
}

// sessionUsername returns the login name of the Wayland session owner.
func sessionUsername() string {
	if os.Getuid() == 0 {
		if u := os.Getenv("SUDO_USER"); u != "" {
			return u
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

// sessionHome returns the home directory of the Wayland session owner.
func sessionHome() string {
	if os.Getuid() == 0 {
		if u := os.Getenv("SUDO_USER"); u != "" {
			return "/home/" + u
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/root"
}

// hyprDir returns the path to the Hyprland socket directory for the session.
func hyprDir() string {
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	if xdg == "" {
		xdg = fmt.Sprintf("/run/user/%d", sessionUID())
	}
	return filepath.Join(xdg, "hypr")
}
