# TODO

Roadmap for wepapered — what's planned or in progress. See the git history for
the full changelog.

## Priorities

- [ ] **Scene shader rendering (incomplete).** The scene render path in LWE does
   not yet cover the full shader set, so some scene wallpapers render wrong or
   only partially. Complete the shader pipeline (the GLSL passes and effects) on
   the LWE C++ side.

- [ ] **Desktop support beyond Hyprland.** Output enumeration and the session
   environment currently assume Hyprland (`hyprctl`). Abstract the compositor
   layer so other Wayland window managers / desktops work.

## Other known work

- [ ] **Hybrid GPU systems (NVIDIA + Intel).** The NVIDIA EGL force in the GPU
  auto-detection assumes a single GPU; detect the active render device.
- [ ] **Web GPU toggle in settings** (auto / hardware / software) so it can be
  changed without environment variables.

## Done

- [x] **Wallpaper properties** — saved, loaded, applied per wallpaper, with the
  property panel populated and editable in the browse UI.
- [x] **Playlists** — host-driven per-monitor rotation (timer / daytime / day-of-week).
- [x] **Monitor / display profile persistence** (Save / Load profile).
- [x] **Packaging + universal installer** — `install.sh` (any distro), the AUR
  `wepapered-bin` / `wepapered-git` packages, and a `systemd --user` service.
  Replaces the old "make install" idea.
- [x] **CI** — builds the daemon (and LWE) and publishes a
  self-contained bundle release; the PR check vets/tests/shellchecks too.
- [x] **Random local UI port + Unix control socket** (no fixed, guessable port).
- [x] **Workshop without a Steam API key** — clear in-UI prompt that opens
  Settings, plus a "?" link to create a key.
- [x] **First-run config** auto-generated; Wallpaper Engine install auto-detected.
- [x] **Audio-reactive web wallpapers.** `window.wallpaperRegisterAudioListener`
  is not implemented, so those wallpapers error and do not react to sound.
- [x] **Add a LICENSE file**
