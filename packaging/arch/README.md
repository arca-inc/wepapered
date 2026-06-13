# Packaging (AUR)

Local AUR style packaging for wepapered. Not published yet; this is for building
and testing the package locally before publishing.

## Files

* PKGBUILD (the wepapered-git VCS package)
* wepapered.desktop, wepapered-settings.desktop (application launcher entries)
* wepapered.service (systemd user service)
* wepapered.install (post install message)

## Install layout

Everything lands in /usr/lib/wepapered (the four binaries plus the LWE runtime
and libcef), with the four commands symlinked into /usr/bin. Colocation matters:
each binary finds the others and libcef relative to its own path ($ORIGIN), and
the renderer finds linux-wallpaperengine next to itself. The symlinks resolve
back into /usr/lib/wepapered, so the lookup still works when launched as
wepaperedctl from PATH.

## Build and test locally

From this directory:

    ./build.sh

While the repos are private, the wrapper rewrites https://github.com/ to SSH for
the build's git operations only, so cloning works over your SSH key. It defaults
to "makepkg -si"; pass other makepkg flags as arguments (for example
"./build.sh -Ri" to repackage and install without rebuilding). makepkg uses its
standard src/ and pkg/ work directories here (both gitignored).

It clones the repo and the lwe submodule, builds the patched
linux-wallpaperengine library (which downloads CEF) and the four Go binaries,
packages them, then installs.

Once the repos are public (or with your own SSH git config), plain "makepkg -si"
works too.

To test against your local working tree instead of the remote (for example
before pushing), point the source at the local repo in PKGBUILD:

    source=("wepapered::git+file://${startdir}/../..")

Regenerate .SRCINFO whenever PKGBUILD metadata changes:

    makepkg --printsrcinfo > .SRCINFO

After install, enable the daemon: systemctl --user enable --now wepapered, and
launch the browse window from the "Wepapered" application entry.

## Notes and TODO before publishing

* The lwe submodule (arca-inc/lwe-patched) is private; the build clones it, so
  makepkg needs git access until that repo is public.
* The package is large. libcef alone is over a gigabyte; the runtime set is
  already trimmed (the glslang, SPIRV, QuickJS and FFT build tools are not
  shipped).
* CEF runtime dependencies beyond nss are not all listed in depends yet. Audit
  with ldd on the installed libcef.so and linux-wallpaperengine before publishing.
* No LICENSE exists upstream yet. The AUR requires one (the package installs it
  when present, see PKGBUILD). Add a LICENSE, then set the license field.
* CEF is downloaded during build() by the LWE CMake, not declared as a source.
  This is a known AUR compliance gap (see TODO.md, automatic build).
* steam UGC helper (wepapered-steam-ugc) is a separate build (needs the Steam
  SDK) and is not packaged here yet.
* No icon is shipped; the desktop entries use the stock
  preferences-desktop-wallpaper icon. Add a wepapered icon when one exists.
