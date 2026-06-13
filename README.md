# wepapered

Native Wallpaper Engine wallpapers on Linux.

wepapered runs the official Wallpaper Engine UI (under Proton) so you browse and
pick wallpapers in its real interface, intercepts your selection over a
WebSocket, and renders it natively per monitor through linux-wallpaperengine
(LWE) on a Hyprland and Wayland desktop.

## What it is

You browse and pick wallpapers in Wallpaper Engine's own UI. wepapered watches
those selections, resolves the wallpaper, and renders it natively (no Wallpaper
Engine process is kept running for display). It also writes your choices back
into Wallpaper Engine's config so the two stay in sync, and re-asserts them if
Wallpaper Engine clears them.

## How it works

The flow runs in one direction:

1. The Wallpaper Engine UI (a small JS spy injected into it) sends the selection.
2. A WebSocket server on 127.0.0.1:9001 (path /we) receives it.
3. The daemon updates and persists its state.
4. One linux-wallpaperengine subprocess per Wayland output renders the wallpaper.

Scene, video, image, and web (CEF) wallpapers are supported. Web wallpapers
render WebGL through ANGLE, with hardware acceleration selected automatically.

## Requirements

* Hyprland on Wayland (hyprctl is used to enumerate the outputs).
* The official Wallpaper Engine, installed through Steam and run under Proton.
* hyprpaper (used to paint the loading placeholder).
* A normal session user (not root, because the embedded LWE and CEF cannot reach
  Wayland as root).
* Build tooling: Go (1.21 or newer), CMake, a C and C++ toolchain, and the GTK3
  and webkit2gtk-4.1 development packages.

## Build

wepapered is a CGo project that links a prebuilt LWE shared library (the lwe
submodule). Build the library first, then the binaries.

```bash
# 1. Init the submodule (first checkout only).
git submodule update --init --recursive

# 2. Build the LWE library and helper binaries into lwe/build/output.
cd lwe && mkdir -p build && cd build
cmake -DCMAKE_BUILD_TYPE=Release ..
make
cd ../..

# 3. Build the four binaries into ./bin.
make
```

The LWE submodule pins CEF 135 by default (its Alloy runtime renders windowless
web wallpapers cleanly). CEF 148 is available with `cmake -DCEF_RELEASE=148`
(experimental, see TODO.md).

## Run

Run the daemon (it detects the GPU and applies your saved wallpapers):

```bash
./bin/wepapered-daemon
```

Or go through the dispatcher:

```bash
./bin/wepaperedctl daemon      # the background renderer
./bin/wepaperedctl gui         # the Wallpaper Engine browse window
./bin/wepaperedctl settings    # the settings window
```

The daemon also runs a system tray with the same actions.

## Configuration

* Config: ~/.config/wepapered/config.json (Wallpaper Engine path, Steam API key,
  UI theme, loading backend, custom directories).
* State: ~/.config/wepapered/state.json (the active wallpaper per monitor).
* The Wallpaper Engine install path is detected from common Steam locations, or
  set it in the settings window.

## Architecture

Four binaries from one Go module, split so each links only the native libraries
it needs:

* wepapered-daemon (links the LWE library): the renderer, WebSocket server,
  browse UI server, config watcher, and tray.
* wepapered-gui (links webkit2gtk): the WebKit browse window.
* wepapered-settings (links GTK3): the settings window.
* wepaperedctl (no native dependencies): a thin dispatcher.

Shared pure Go state lives in internal/core; the daemon subsystems in
internal/daemon. See CLAUDE.md for the deep architecture and rendering notes.

## Status

Working today: native rendering per monitor, hot swapping, scene, video, image,
and web wallpapers, hardware accelerated WebGL (detected automatically), the
native browse window, the settings window, and the tray.

See TODO.md for what is in progress, and MAINTAINERS.md for who looks after what.
