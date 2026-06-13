# wepapered — installation

🇬🇧 *English version: [INSTALL.md](INSTALL.md) (in the archive) / [RELEASE.md](packaging/RELEASE.md) (repo).*

Wallpaper Engine rendu nativement sur **Hyprland / Wayland**. Cette archive est
**autonome** : ffmpeg, mpv et les codecs sont embarqués, donc elle tourne quelle
que soit la version ffmpeg de ta distribution.

## Prérequis (côté système)

- **Hyprland** en cours d'exécution (le daemon appelle `hyprctl`).
- Une session **Wayland**, lancée en tant qu'**utilisateur** de la session (pas root).
- **Wallpaper Engine** installé (via Steam/Proton) pour parcourir et choisir les fonds.
- Paquets système usuels d'un bureau : pilote GPU / Mesa (`libGL`/`libEGL`),
  `wayland`, `gtk3` + `webkit2gtk-4.1` (fenêtre de navigation), `nss` (CEF),
  `fontconfig`/`freetype`, `dbus`, et un serveur audio (`pulseaudio`/`pipewire`).
- Optionnel : `hyprpaper` ou `swww` pour l'image de chargement.

## Installation

```bash
tar -xzf wepapered-linux-amd64.tar.gz
cd wepapered

# Lancer le daemon (rend les fonds + sert l'UI)
./wepapered-daemon

# Dans un autre terminal :
./wepapered-gui        # fenêtre de navigation Wallpaper Engine
./wepapered-settings   # réglages (chemin WE, clé API, thème…)
```

`wepaperedctl <daemon|gui|settings>` fait la même chose via un dispatcher unique.

Pour une installation système (binaires dans `/opt`, lanceurs `.desktop`,
service `systemd --user`), voir `packaging/arch/PKGBUILD` (paquet AUR pour Arch).

## Notes

- Garder les binaires **ensemble** : ils se localisent les uns les autres et la
  bibliothèque LWE via `$ORIGIN` (chemins relatifs au binaire).
- Le chemin de Wallpaper Engine est auto-détecté depuis les emplacements Steam
  courants ; sinon, renseigne-le dans **Réglages**.
- L'abonnement/téléchargement Workshop en direct nécessite le client **Steam**
  ouvert et connecté.
