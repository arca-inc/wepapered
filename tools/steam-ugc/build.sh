#!/usr/bin/env bash
# Build the Steam UGC helper. Links against the Steam client's redistributable
# libsteam_api.so (found in the local Steam install) and rpaths to it.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:-$HERE/../../lwe/build/output}"

# Locate libsteam_api.so from the Steam install.
STEAM_ROOT="${STEAM_ROOT:-$HOME/.local/share/Steam}"
LIBDIR=""
for d in "$STEAM_ROOT/steamrt64" "$STEAM_ROOT/linux64"; do
    [ -f "$d/libsteam_api.so" ] && LIBDIR="$d" && break
done
if [ -z "$LIBDIR" ]; then
    LIBDIR="$(dirname "$(find "$STEAM_ROOT" -name libsteam_api.so 2>/dev/null | head -1)")"
fi
[ -n "$LIBDIR" ] || { echo "libsteam_api.so not found under $STEAM_ROOT" >&2; exit 1; }

mkdir -p "$OUT"
gcc "$HERE/steam_ugc.c" -o "$OUT/wepapered-steam-ugc" \
    -L"$LIBDIR" -lsteam_api -Wl,-rpath,"$LIBDIR"
echo "built $OUT/wepapered-steam-ugc (linked $LIBDIR/libsteam_api.so)"
