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

### Quick (any distro)

```bash
curl -fsSL https://raw.githubusercontent.com/arca-inc/wepapered/master/install.sh | sh
```

Installs the same self-contained bundle on every distro — so it works the same
everywhere, including unsupported ones (Fedora, openSUSE, …). Defaults to a
per-user install under `~/.local` (no root); pass `--system` for `/usr/local`. It
installs `.desktop` launchers and a `systemd --user` service, and offers to pull
the host runtime libraries (gtk3 / webkit2gtk / nss) via your package manager.
Options:

```bash
curl -fsSL .../install.sh | sh -s -- --system          # system-wide (/usr/local)
curl -fsSL .../install.sh | sh -s -- --version v1.2.3   # pin a release
curl -fsSL .../install.sh | sh -s -- --uninstall        # remove
```

### Manual (from the archive)

```bash
tar -xzf wepapered-linux-amd64.tar.gz
cd wepapered
./install.sh                 # same installer, bundled in the archive
# …or run in place, no install:
./wepapered-daemon           # renders wallpapers + serves the UI
./wepapered-gui              # Wallpaper Engine browse window
./wepapered-settings         # settings (WE path, API key, theme…)
```

`wepaperedctl <daemon|gui|settings>` does the same through a single dispatcher.

### Arch Linux

`wepapered-bin` (prebuilt, this bundle) or `wepapered-git` (builds from source) —
see `packaging/arch/`. (The `install.sh` one-liner above always installs the
universal bundle; use these for native pacman integration.)

## Notes

- Keep the binaries **together**: they locate each other and the LWE library via
  `$ORIGIN` (paths relative to the binary).
- The Wallpaper Engine path is auto-detected from common Steam locations;
  otherwise set it in **Settings**.
- Live Workshop subscribe/download needs the **Steam** client open and logged in.
