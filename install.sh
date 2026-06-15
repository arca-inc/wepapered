#!/bin/sh
# wepapered universal installer.
#
#   curl -fsSL https://raw.githubusercontent.com/arca-inc/wepapered/master/install.sh | sh
#
# Installs wepapered on any Linux distro from a single self-contained prebuilt
# bundle (the binaries + the LWE runtime + libcef + the media-codec closure, all
# bundled), downloaded from the GitHub release — so it works the same everywhere,
# including unsupported distros (Fedora, openSUSE, …). The host runtime libraries
# it does need (gtk3 / webkit2gtk / nss) are installed through the detected
# package manager when possible.
#
# Defaults to a per-user install under ~/.local (no root). Pass --system for a
# system-wide install under /usr/local (uses sudo).
#
# Env overrides: WEPAPERED_REPO, WEPAPERED_VERSION (release tag; "latest" = the
# rolling build), WEPAPERED_TOKEN (GitHub token, only needed while the repo's
# releases are private).
set -eu

REPO="${WEPAPERED_REPO:-arca-inc/wepapered}"
VERSION="${WEPAPERED_VERSION:-latest}"
ASSET="wepapered-linux-amd64.tar.gz"
TOKEN="${WEPAPERED_TOKEN:-}"   # explicit only; not inherited from ambient GITHUB_TOKEN
SYSTEM=0
ASSUME_YES=0
DO_DEPS=auto      # auto|yes|no — install host runtime deps via the package manager
UNINSTALL=0

# ── output helpers ──────────────────────────────────────────────────────────
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	C_B=$(printf '\033[1m'); C_G=$(printf '\033[32m'); C_Y=$(printf '\033[33m')
	C_R=$(printf '\033[31m'); C_0=$(printf '\033[0m')
else
	C_B=; C_G=; C_Y=; C_R=; C_0=
fi
msg()  { printf '%s\n' "${C_G}::${C_0} $*"; }
step() { printf '%s\n' "${C_B}==>${C_0} $*"; }
warn() { printf '%s\n' "${C_Y}warning:${C_0} $*" >&2; }
err()  { printf '%s\n' "${C_R}error:${C_0} $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

usage() {
	cat <<EOF
wepapered installer

usage: install.sh [options]

  --system           install system-wide under /usr/local (uses sudo)
  --user             install under ~/.local (default)
  --version TAG      release tag to install        (default: latest)
  --deps yes|no      install host runtime deps via the package manager
  --uninstall        remove a previous install
  -y, --yes          assume yes to prompts (non-interactive)
  -h, --help         this help

env: WEPAPERED_REPO WEPAPERED_VERSION WEPAPERED_TOKEN
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--system) SYSTEM=1 ;;
		--user) SYSTEM=0 ;;
		--version) VERSION="${2:?}"; shift ;;
		--version=*) VERSION="${1#*=}" ;;
		--deps) DO_DEPS="${2:?}"; shift ;;
		--deps=*) DO_DEPS="${1#*=}" ;;
		--uninstall) UNINSTALL=1 ;;
		-y|--yes) ASSUME_YES=1 ;;
		-h|--help) usage; exit 0 ;;
		*) err "unknown option: $1 (see --help)" ;;
	esac
	shift
done

confirm() { # confirm "question" — default yes
	[ "$ASSUME_YES" = 1 ] && return 0
	# Prompt via the controlling terminal even when stdin is the piped script
	# (curl … | sh). With no tty at all, take the default (yes).
	if [ -r /dev/tty ]; then
		printf '%s [Y/n] ' "$1" >/dev/tty
		read -r ans </dev/tty || return 0
		case "$ans" in [nN]*) return 1 ;; *) return 0 ;; esac
	fi
	return 0
}

# ── prefix / paths ──────────────────────────────────────────────────────────
if [ "$SYSTEM" = 1 ]; then
	PREFIX=/usr/local
	if [ "$(id -u)" = 0 ]; then SUDO=; else
		have sudo || err "--system needs root or sudo"
		SUDO=sudo
	fi
else
	PREFIX="${HOME}/.local"
	SUDO=
fi
LIBDIR="${PREFIX}/lib/wepapered"
BINDIR="${PREFIX}/bin"
APPDIR="${PREFIX}/share/applications"
CMDS="wepaperedctl wepapered-daemon wepapered-gui wepapered-settings"
# The systemd unit is always a per-user service.
UNITDIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

run() { # run a privileged command honoring SUDO
	if [ -n "$SUDO" ]; then $SUDO "$@"; else "$@"; fi
}

# ── distro detection (only to install host deps via the right package manager) ─
FAMILY=unknown
PKG=
detect_distro() {
	[ -r /etc/os-release ] || return 0
	# Read ID/ID_LIKE in a subshell instead of sourcing os-release into our scope:
	# os-release also defines VERSION= (e.g. "2.18" on Gentoo), which would clobber
	# the release tag we resolved above and break the download.
	# shellcheck disable=SC1091
	ids=$(. /etc/os-release 2>/dev/null; printf '%s %s' "${ID:-}" "${ID_LIKE:-}")
	for id in $ids; do
		case "$id" in
			arch|cachyos|manjaro|endeavouros|garuda|artix) FAMILY=arch; break ;;
			debian|ubuntu|linuxmint|pop|raspbian) FAMILY=debian; break ;;
			fedora|rhel|centos|rocky|almalinux|nobara) FAMILY=fedora; break ;;
			opensuse*|suse|sles) FAMILY=suse; break ;;
		esac
	done
	for p in pacman apt-get dnf zypper; do have "$p" && { PKG=$p; break; }; done
	return 0   # never let a failed `have` abort the script under set -e
}

# Host runtime libraries (the bundle ships ffmpeg/mpv/codecs/CEF itself; the gui
# and settings windows use the HOST gtk/webkit stack, and CEF needs host nss).
deps_for_family() {
	# webkit2gtk depends on the gtk3 runtime, so it pulls gtk3 in transitively —
	# we don't list gtk3 explicitly on Debian (its runtime was renamed to
	# libgtk-3-0t64 by the time_t transition, so a hardcoded libgtk-3-0 breaks on
	# trixie/24.04+). openSUSE uses Debian-style lib-prefixed runtime names.
	case "$FAMILY" in
		arch)   echo "gtk3 gtk-layer-shell webkit2gtk-4.1 nss" ;;
		debian) echo "libgtk-layer-shell0 libwebkit2gtk-4.1-0 libnss3" ;;
		fedora) echo "gtk3 gtk-layer-shell webkit2gtk4.1 nss" ;;
		suse)   echo "libgtk-3-0 libgtk-layer-shell0 libwebkit2gtk-4_1-0 mozilla-nss" ;;
		*)      echo "" ;;
	esac
}

install_deps() {
	[ "$DO_DEPS" = no ] && return 0
	deps=$(deps_for_family)
	if [ -z "$PKG" ] || [ -z "$deps" ]; then
		warn "unknown package manager/distro — install host deps yourself: gtk3, gtk-layer-shell, webkit2gtk-4.1, nss"
		return 0
	fi
	if [ "$DO_DEPS" = auto ]; then
		confirm "Install host runtime deps via ${PKG} (needs sudo)? [${deps}]" || return 0
	fi
	have sudo || [ "$(id -u)" = 0 ] || { warn "no sudo; skipping dep install"; return 0; }
	S=; [ "$(id -u)" = 0 ] || S=sudo
	step "Installing host dependencies via ${PKG}"
	[ "$PKG" = apt-get ] && { $S apt-get update || warn "apt-get update failed; continuing"; }
	# Install each package on its own so one missing/renamed name (distro naming
	# drifts) doesn't abort the whole transaction and leave the rest uninstalled.
	for d in $deps; do
		case "$PKG" in
			pacman)  $S pacman -S --needed --noconfirm "$d" ;;
			apt-get) $S apt-get install -y "$d" ;;
			dnf)     $S dnf install -y "$d" ;;
			zypper)  $S zypper install -y "$d" ;;
		esac || warn "could not install ${d}; continuing"
	done
}

# ── download ────────────────────────────────────────────────────────────────
download() { # download URL OUT
	url="$1"; out="$2"
	# A token is only needed for private releases. curl strips the Authorization
	# header on the cross-host redirect to GitHub's asset CDN; wget does NOT, so it
	# would leak the token — require curl whenever a token is in play.
	if [ -n "$TOKEN" ]; then
		have curl || err "WEPAPERED_TOKEN requires curl (wget would forward it across the redirect)"
		curl -fSL -H "Authorization: Bearer ${TOKEN}" -o "$out" "$url"
	elif have curl; then
		curl -fSL -o "$out" "$url"
	elif have wget; then
		wget -O "$out" "$url"
	else
		err "need curl or wget to download"
	fi
}

# ── desktop entries + systemd user service ──────────────────────────────────
write_desktop_and_service() {
	# Desktop entries (absolute Exec/Icon so they work regardless of PATH).
	run mkdir -p "$APPDIR"
	tmpf=$(mktemp)
	cat >"$tmpf" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=Wepapered
GenericName=Wallpaper Engine
Comment=Browse and apply Wallpaper Engine wallpapers
Exec=${BINDIR}/wepaperedctl gui
Icon=${LIBDIR}/assets/logo.png
Terminal=false
Categories=Graphics;
Keywords=wallpaper;wallpaperengine;desktop;background;
StartupNotify=false
StartupWMClass=wepapered-gui
EOF
	run install -Dm644 "$tmpf" "${APPDIR}/wepapered.desktop"
	cat >"$tmpf" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=Wepapered Settings
Comment=Configure wepapered (WE path, Steam API key, theme)
Exec=${BINDIR}/wepaperedctl settings
Icon=${LIBDIR}/assets/logo.png
Terminal=false
Categories=Graphics;Settings;
NoDisplay=false
StartupWMClass=wepapered-settings
EOF
	run install -Dm644 "$tmpf" "${APPDIR}/wepapered-settings.desktop"
	rm -f "$tmpf"

	# systemd user service (always per-user, regardless of --system).
	mkdir -p "$UNITDIR"
	cat >"${UNITDIR}/wepapered.service" <<EOF
[Unit]
Description=wepapered (Wallpaper Engine bridge for linux-wallpaperengine)
Documentation=https://github.com/${REPO}
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=${BINDIR}/wepapered-daemon
Restart=on-failure
RestartSec=5s
Environment=XDG_SESSION_TYPE=wayland
PassEnvironment=WAYLAND_DISPLAY XDG_RUNTIME_DIR HYPRLAND_INSTANCE_SIGNATURE DISPLAY
StandardOutput=journal
StandardError=journal
SyslogIdentifier=wepapered

[Install]
WantedBy=graphical-session.target
EOF
}

# ── tarball install ─────────────────────────────────────────────────────────
install_via_tarball() {
	[ "$(uname -m)" = x86_64 ] || err "no prebuilt bundle for $(uname -m); only x86_64 is published"
	have tar || err "need tar"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	url="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
	step "Downloading ${ASSET} (${VERSION})"
	download "$url" "$tmp/$ASSET" || err "download failed: $url"

	# Integrity: verify against the published .sha256 when available. Hard-fail on
	# mismatch; only warn if the release predates checksums or sha256sum is absent.
	if have sha256sum && download "${url}.sha256" "$tmp/$ASSET.sha256" 2>/dev/null; then
		step "Verifying checksum"
		( cd "$tmp" && sha256sum -c "$ASSET.sha256" ) || err "checksum verification failed"
	else
		warn "no checksum available for this release — skipping integrity check"
	fi

	# Reject archives with absolute or traversing ('..') members before extracting.
	if tar -tzf "$tmp/$ASSET" | grep -Eq '(^|/)\.\.(/|$)|^/'; then
		err "refusing unsafe archive (absolute or '..' paths)"
	fi
	step "Extracting"
	tar --no-same-owner -xzf "$tmp/$ASSET" -C "$tmp"
	[ -d "$tmp/wepapered" ] || err "unexpected archive layout (no wepapered/ dir)"

	step "Installing to ${LIBDIR}"
	run rm -rf "$LIBDIR"
	run mkdir -p "$(dirname "$LIBDIR")" "$BINDIR"
	run cp -a "$tmp/wepapered" "$LIBDIR"

	# User commands → $BINDIR, symlinked into the lib dir. Go's os.Executable()
	# resolves the symlink, so colocated sibling/LWE lookup still works.
	for c in $CMDS; do
		[ -f "${LIBDIR}/${c}" ] || { warn "missing ${c} in bundle"; continue; }
		run chmod +x "${LIBDIR}/${c}"
		run ln -sf "${LIBDIR}/${c}" "${BINDIR}/${c}"
	done

	write_desktop_and_service
	rm -rf "$tmp"; trap - EXIT
}

# ── uninstall ───────────────────────────────────────────────────────────────
do_uninstall() {
	step "Uninstalling wepapered"
	systemctl --user disable --now wepapered 2>/dev/null || true
	rm -f "${UNITDIR}/wepapered.service"
	for c in $CMDS; do run rm -f "${BINDIR}/${c}"; done
	run rm -rf "$LIBDIR"
	run rm -f "${APPDIR}/wepapered.desktop" "${APPDIR}/wepapered-settings.desktop"
	msg "Removed (config in ~/.config/wepapered left intact)."
}

# ── path hint ───────────────────────────────────────────────────────────────
path_hint() {
	case ":${PATH}:" in
		*":${BINDIR}:"*) ;;
		*)
			warn "${BINDIR} is not on your PATH — add it:"
			# $PATH is intentionally literal here (it's for the user to paste).
			# shellcheck disable=SC2016
			printf '       export PATH="%s:$PATH"\n' "$BINDIR" >&2 ;;
	esac
}

# ── main ────────────────────────────────────────────────────────────────────
detect_distro

if [ "$UNINSTALL" = 1 ]; then do_uninstall; exit 0; fi

step "wepapered installer (${FAMILY}, ${PKG:-no package manager}, prefix ${PREFIX})"

install_deps
install_via_tarball

path_hint
cat <<EOF

${C_G}wepapered installed.${C_0}

  Start the daemon:   systemctl --user enable --now wepapered
            or:       wepapered-daemon
  Browse wallpapers:  wepaperedctl gui     (or the "Wepapered" launcher)
  Settings:           wepaperedctl settings

Needs Hyprland + a Wayland session, and Wallpaper Engine (Steam/Proton) to pick
wallpapers. systemd user services may need the session env:
  systemctl --user import-environment WAYLAND_DISPLAY XDG_RUNTIME_DIR HYPRLAND_INSTANCE_SIGNATURE
EOF
