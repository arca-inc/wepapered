# TODO

What is planned or in progress for wepapered. 

## Priorities

- [] Wallpaper properties (save, load, and apply). Persist each wallpaper's user
   properties (the combos, sliders, and colors defined in project.json), load
   them together with the wallpaper, and apply them on render. Today overrides
   are passed through but not fully saved and restored per wallpaper.

- [] Scene shader rendering (incomplete). The scene render path in LWE does not yet
   cover the full shader set, so some scene wallpapers render wrong or only
   partially. Complete the shader pipeline (the GLSL passes and effects) on the
   LWE C++ side.

- [] A lighter webkit. CEF (full Chromium) is heavy (libcef alone is over a
   gigabyte). Evaluate a lighter offscreen web engine (WPE WebKit is the main
   candidate) or trim CEF, while keeping WebGL, the wp:// scheme, and input
   working. See the CEF research notes referenced in CLAUDE.md.

- [] Automatic build in GitHub (CI). The workflow is started
   (.github/workflows/build.yml) but fails on dependency and library problems for
   the recursive project (the LWE submodule, the CEF download, and the GLEW or
   EGL build dependencies). Make CI build the submodule and the four binaries
   reliably.

## Other known work

* Audio reactive web wallpapers. window.wallpaperRegisterAudioListener is not
  implemented, so those wallpapers error and do not react to sound.
* Hybrid GPU systems (NVIDIA together with Intel). The NVIDIA EGL force in the
  GPU auto detection assumes a single GPU; detect the active render device.
* A web GPU toggle in the settings window (auto, hardware, or software) so it can
  be changed without environment variables.
* A make install target (place the four binaries in ~/.local/bin and install
  wepapered.service).

## Nice to have

* Packaging (an install script or a distro package) so users do not build LWE by
  hand.
* Desktop support beyond Hyprland (output enumeration currently shells out to
  hyprctl).
* Add a LICENSE file.
