# wepapered — installation

🇫🇷 *Version française : [INSTALL_FR.md](INSTALL_FR.md) (in the archive) / [RELEASE_FR.md](packaging/RELEASE_FR.md) (repo).*

Wallpaper Engine rendered natively on **Hyprland / Wayland**. This archive is
**self-contained**: ffmpeg, mpv and the codecs are bundled, so it runs whatever
ffmpeg version your distribution ships.

## Requirements (host side)

- **Hyprland** running (the daemon shells out to `hyprctl`).
- A **Wayland** session, started as the **session user** (not root).
- **Wallpaper Engine** installed (via Steam/Proton) to browse and pick wallpapers.
- Usual desktop system packages: GPU driver / Mesa (`libGL`/`libEGL`),
  `wayland`, `gtk3` + `webkit2gtk-4.1` (browse window), `nss` (CEF),
  `fontconfig`/`freetype`, `dbus`, and an audio server (`pulseaudio`/`pipewire`).
- Optional: `hyprpaper` or `swww` for the loading placeholder.

## Install

```bash
tar -xzf wepapered-linux-amd64.tar.gz
cd wepapered

# Start the daemon (renders wallpapers + serves the UI)
./wepapered-daemon

# In another terminal:
./wepapered-gui        # Wallpaper Engine browse window
./wepapered-settings   # settings (WE path, API key, theme…)
```

`wepaperedctl <daemon|gui|settings>` does the same through a single dispatcher.

For a system install (binaries under `/opt`, `.desktop` launchers, a
`systemd --user` service), see `packaging/arch/PKGBUILD` (AUR package for Arch).

## Notes

- Keep the binaries **together**: they locate each other and the LWE library via
  `$ORIGIN` (paths relative to the binary).
- The Wallpaper Engine path is auto-detected from common Steam locations;
  otherwise set it in **Settings**.
- Live Workshop subscribe/download needs the **Steam** client open and logged in.
