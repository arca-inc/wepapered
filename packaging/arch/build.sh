#!/usr/bin/env bash
# Run makepkg with the PRIVATE arca-inc github repos (the wepapered repo and its
# lwe submodule) rewritten from https to SSH, so they clone over your SSH key,
# while the public nested submodules clone over https.
#
# We ignore your global gitconfig for the build (GIT_CONFIG_GLOBAL=/dev/null)
# because a global "url.git@github.com:.insteadOf = https://github.com/" forces
# *every* github URL through SSH, and GitHub throttles the resulting burst of SSH
# clones, leaving some public submodules empty (cmake then fails on e.g. argparse).
# A scoped arca-inc-only rewrite is re-added on top. The PKGBUILD itself stays on
# https, so it remains publishable to the AUR unchanged.
#
# makepkg uses its standard src/ and pkg/ work dirs here (both gitignored).
# Defaults to "makepkg -si"; pass other makepkg flags as arguments
# (for example "./build.sh -Ri" to repackage and install without rebuilding).
set -euo pipefail
cd -- "$(dirname -- "$0")"

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0='url.git@github.com:arca-inc/.insteadOf'
export GIT_CONFIG_VALUE_0='https://github.com/arca-inc/'

args=("$@")
[ ${#args[@]} -eq 0 ] && args=(-si)
makepkg "${args[@]}"
