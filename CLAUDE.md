# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`wepapered` is a Go daemon that bridges the official **Wallpaper Engine** (the Windows app, run under Proton/Wine on Linux) to **linux-wallpaperengine (LWE)** for actual rendering on a Hyprland/Wayland desktop. The user browses and picks wallpapers in WE's real UI; wepapered intercepts those selections over a WebSocket and renders them natively via per-monitor LWE subprocesses.

The flow: **WE UI (JS spy) → WebSocket → daemon state → LWE subprocess per monitor → Wayland output**. The daemon also writes selections back into WE's own `config.json` so WE remembers them, and watches that file to re-assert state when WE clears it.

## Build & run

This is a **CGo** project that links against a prebuilt LWE shared library (`lwe/` git submodule, the `arca-inc/lwe-patched` fork on the `wepapered` branch). The LWE library must be built first.

```bash
# 1. Init the submodule (first checkout only)
git submodule update --init --recursive

# 2. Build the LWE library + helper binaries into lwe/build/output/
cd lwe && mkdir -p build && cd build
cmake -DCMAKE_BUILD_TYPE=Release ..
make                       # produces liblinux-wallpaperengine-lib.so, linux-wallpaperengine, lwe-cef-subprocess

# 3. Build the four binaries into ./bin (CGo cflags/ldflags in
#    internal/daemon/lwe.go point at lwe/build/output).
cd /home/davidutz/personal/wepapered
make                   # → bin/{wepapered-daemon,wepapered-gui,wepapered-settings,wepaperedctl}

# Run directly, or via the dispatcher: `bin/wepaperedctl <daemon|gui|settings>`
./bin/wepapered-daemon     # the background daemon (renders wallpapers, serves the UI)
./bin/wepapered-gui        # WebKit browse window (starts the daemon if none is running)
./bin/wepapered-settings   # GTK3 settings window (WE path, API key, theme, custom dirs)
```

There is **no test suite or linter config** in this repo; `.github/workflows/build.yml` builds the binaries on push. `make vet` (`go vet ./...`) and `go build ./...` are the local checks.

Runtime requirements: must run as the **session user** (not root — embedded LWE/CEF cannot reach Wayland as root; the code has fallbacks using `sudo -u`/`SUDO_USER` but the normal path is the logged-in user). Hyprland must be running (`hyprctl` is shelled out to). `hyprpaper` is used for loading placeholders.

## Key paths & environment

- WE install path: stored in `~/.config/wepapered/config.json`; auto-detected from common Steam locations (`internal/core/config.go:AutoDetectWEPath`).
- Daemon state (active wallpaper per monitor): `~/.config/wepapered/state.json` (`internal/daemon/state.go`).
- WebSocket server: `127.0.0.1:9001`, path `/we` (`internal/daemon/wsserver.go`). The WE UI must be injected with a JS spy that connects here — that injection lives in the patched WE/LWE side, not this repo.
- `LWE_OUTPUT_DIR` env var overrides where the LWE binaries are found (`internal/core/paths.go`); defaults to `lwe/build/output` in dev, or the dir next to the running executable when packaged.
- Per-screen IPC sockets: `/tmp/wepapered-ctrl-<output>.sock`. Loading placeholder image: `/tmp/wepapered-loading.png`.

## Architecture

Four binaries built from one module, split along their native-dependency lines so
each links only what it needs (verified: `wepapered-gui` cannot pull in the LWE
renderer, which is what made the old single-binary `--gui` spawn a second daemon):

- **`cmd/wepapered-daemon`** → `internal/daemon` (links the LWE library) — the background service.
- **`cmd/wepapered-gui`** (links webkit2gtk) — the WebKit browse window; ensures the daemon is up, then opens the UI it serves.
- **`cmd/wepapered-settings`** (links gotk3) — the GTK3 settings window.
- **`cmd/wepaperedctl`** — a tiny dispatcher that execs the binary for `daemon`/`gui`/`settings`.
- **`internal/core`** — pure-Go shared state (config, WE-path resolution, LWE/companion-binary paths, browse-UI URL). Links no CGo, so gui/settings/ctl import it without dragging in the LWE library.

The daemon's subsystems live in `internal/daemon`, one file each. `aliases.go`
re-exports the `core` symbols under their original unqualified names (`Config`,
`loadConfig`, `lwebin`, …) so the files below read as they did pre-split:

- **`run.go`** — daemon entry point (`daemon.Run`). Binds the control port as a single-instance gate, wires up `WSServer` → `Renderer` → `Watcher`, applies saved state on startup, and runs the watchdog loop that drains `renderer.applyTrigger`.
- **`wsserver.go`** — WebSocket server that the WE UI's JS spy connects to. Parses intercepted WE method calls (`browseWallpaperObject.selectWallpaper`, `settingsObject.applyGeneral`). On a selection it resolves metadata, updates state, persists back to WE config, fires a desktop notification, and triggers `renderer.Apply`.
- **`state.go`** — `DaemonState` / `MonitorWallpaper` models, JSON persistence, Windows↔Linux path translation (`winToLinux`: `S:` = steamapps root, `Z:` = Linux root via Wine), `project.json` parsing, and wallpaper **type inference**. Crucial concept: a wallpaper may be a thin "preset" that depends on a separate framework workshop item (`dependency` field) — in that case `RenderDir` points at the framework (HTML/JS) and `PresetDir` at the original wallpaper's assets, with `Props` carrying preset overrides.
- **`renderer.go`** — the heart of the project. Runs **one `linux-wallpaperengine` subprocess per Wayland output**, keyed by output name. `Apply(state)` diffs desired-vs-running and decides per screen: start, stop, or **hot-swap**. Monitor labels `Monitor0/1/…` map to outputs sorted left-to-right by x,y from `hyprctl monitors`.
- **`watcher.go`** — fsnotify watch on WE's `config.json`; when WE clears `selectedwallpapers`, debounce-rewrites our state back into it so WE doesn't win the fight.
- **`weconfig.go`** — writing `selectedwallpapers` back into WE's `config.json`, keyed by Windows device path (from `monitormap`) with `MonitorN` fallback.
- **`lwe.go`** — CGo bridge to `liblinux-wallpaperengine-lib.so` (the `#cgo` include/lib paths are `../../lwe` relative to `internal/daemon`). Note: the embedded `lwe_run` path is **legacy/unused for rendering** — the renderer launches the `linux-wallpaperengine` *binary* as subprocesses instead. CGo is still used for `lwe_set_subprocess_path`.

The GTK3 settings window lives separately in **`cmd/wepapered-settings/main.go`** (it links gotk3, not LWE). The error-wallpaper page it has nothing to do with is in the daemon (`errorWallpaperDir`, French string "Wallpaper non supporté").

### Rendering model (the hard part)

The renderer is concurrency-heavy; read `renderer.go` before touching it. Invariants:

- All `screenProc` map mutation happens under `Renderer.mu`. Methods named `*Locked` assume the lock is held; some release and re-acquire it while waiting on `doneCh`.
- **Hot-swap vs restart**: wallpaper changes prefer a JSON `load` command over the per-screen ctrl socket (`sendCtrlLoadJSON`, LWE replies `READY`). Only if there's no socket / it fails does the process get killed and relaunched.
- **Web (CEF) wallpapers are special-cased**: two CEF processes on the same output share a profile/UKM database and deadlock → black screen. So when the *old* wallpaper is `web`, the swap is **sequential** (kill old, then start new); for non-web it's **parallel** (start new alongside old, retire old only after the new one's first frame) for a seamless transition.
- **READY signaling**: each subprocess gets a pipe on fd 3 and `WEPAPERED_READY_FD=3`; LWE writes `READY` when its first frame paints. Used to time placeholder removal and old-process retirement.
- **Watchdog / crash backoff**: when a subprocess dies unexpectedly it's removed from the map and a re-apply is scheduled with linear backoff, giving up after 5 rapid crashes (counter resets after 30s uptime).
- **Unsupported types** (anything not scene/video/web/image) render a generated "Wallpaper non supporté" web page instead of failing silently (`errorWallpaperDir`).

### Subprocess env (`lweSubprocEnv`)

LWE subprocesses need a carefully constructed Wayland environment: `XDG_SESSION_TYPE=wayland`, `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, the Hyprland instance signature, plus `LD_LIBRARY_PATH`/`LD_PRELOAD` pointing at the LWE output dir (for bundled CEF libs and the ICU fix shim) and `LWE_CEF_SUBPROCESS_PATH` set to the minimal `lwe-cef-subprocess` helper (the full binary would try to init Wayland when CEF spawns it as a renderer and deadlock).

### Web (CEF) GPU backend — auto-detected (`webGPUEnv` in `renderer.go`)

`web`-type wallpapers render WebGL through ANGLE in CEF. `lweSubprocEnv` auto-selects the backend from the system so the daemon "just works" with no env:

- a GPU render node exists (`/dev/dri/renderD*`) → `LWE_WEB_ANGLE=gl-egl` (hardware).
- the NVIDIA proprietary driver is loaded → additionally force its GLVND EGL vendor via `__EGL_VENDOR_LIBRARY_FILENAMES` (else ANGLE loads Mesa's libEGL, which can't drive the NVIDIA card and silently falls back to `llvmpipe` software).
- no GPU → `LWE_WEB_ANGLE=swiftshader` (software).

The C++ side (`lwe/src/.../CEF/BrowserApp.cpp`, `lwe_cef_subprocess.cpp`) reads `LWE_WEB_ANGLE` / `LWE_WEB_OZONE` and gates GPU enablement; `LWE_WEB_GPU_LOG=1` streams Chromium GPU logs + logs the page's actual WebGL renderer. **Any of these env vars set manually override the auto-detection.** The renderer also filters the high-volume `Fontconfig warning:` lines CEF's bundled fontconfig emits (`lweLogFilter`).

The web/CEF GPU stack is **CEF-version-sensitive**: the LWE submodule defaults to **CEF 135** (Alloy runtime, clean windowless OSR). CEF 148 (`-DCEF_RELEASE=148`) is Chrome-runtime-only and currently crashes windowless web wallpapers (process-singleton + fatal empty-rect `NOTREACHED`).

## Working with the submodule

`lwe/` is the patched LWE fork. The C-ABI contract between the daemon and LWE is in `lwe/src/lwe_bridge.h` and the ctrl-socket / READY-fd protocol is implemented in `lwe/src/main.cpp` (look for `WEPAPERED_CTRL_SOCK` / `WEPAPERED_READY_FD`). If you change that protocol you must update both `renderer.go` (Go side) and the LWE C++ side, and rebuild the library before rebuilding the daemon.
