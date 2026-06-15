// wepapered-settings — the GTK3 configuration window. Edits the shared config
// (WE path, Steam API key, UI theme, loading backend, custom dirs). Links gotk3
// only; no LWE, no webkit.
package main

import (
	"os/exec"
	"strings"

	"github.com/gotk3/gotk3/gtk"

	"wepapered/internal/core"
)

// listAudioMonitors enumerates PulseAudio/PipeWire monitor sources (the ".monitor"
// of each output sink) via `pactl`, to populate the audio-source picker. Returns nil
// if pactl is unavailable; the picker then just offers "Default (auto)".
func listAudioMonitors() []string {
	out, err := exec.Command("pactl", "list", "sources", "short").Output()
	if err != nil {
		return nil
	}
	var mons []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		if name := strings.TrimSpace(fields[1]); strings.Contains(name, ".monitor") {
			mons = append(mons, name)
		}
	}
	return mons
}

// listAudioApps returns the names of applications currently producing audio (PulseAudio
// sink-inputs), via `pactl list sink-inputs` (application.name). Used to let the user
// target a single app's audio. Returns nil if pactl is unavailable.
func listAudioApps() []string {
	out, err := exec.Command("pactl", "list", "sink-inputs").Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var apps []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		const key = "application.name = "
		if strings.HasPrefix(line, key) {
			if v := strings.Trim(strings.TrimPrefix(line, key), "\""); v != "" && !seen[v] {
				seen[v] = true
				apps = append(apps, v)
			}
		}
	}
	return apps
}

// listMediaPlayers returns the available MPRIS player base names (e.g. "spotify",
// "firefox") via `playerctl --list-all`, with the ".instanceN" suffix stripped and
// duplicates removed. Returns nil if playerctl is unavailable.
func listMediaPlayers() []string {
	out, err := exec.Command("playerctl", "--list-all").Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var players []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if i := strings.Index(name, ".instance"); i >= 0 {
			name = name[:i]
		}
		if !seen[name] {
			seen[name] = true
			players = append(players, name)
		}
	}
	return players
}

// guiSkins lists the selectable WE UI themes as {value, label}. The value is the
// stylesheet base name under ui/dist/styles (see core.GUIURL); the label is shown
// in the config dropdown.
var guiSkins = []struct{ Value, Label string }{
	{"skindark", "Dark (default)"},
	{"skinobsidian", "Obsidian"},
	{"skinspace", "Space"},
	{"skinmetal", "Metal"},
	{"skinmist", "Mist"},
	{"skinmoss", "Moss"},
	{"skinrose", "Rose"},
	{"skinrust", "Rust"},
	{"skinwinter", "Winter"},
	{"skinhalloween", "Halloween"},
	{"main", "Light"},
}

func main() {
	cfg, _ := core.LoadConfig()
	runConfigUI(cfg)
}

func runConfigUI(cfg *core.Config) {
	gtk.Init(nil)

	win, _ := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	win.SetTitle("Wepapered Settings")
	win.SetDefaultSize(560, 460)
	win.SetBorderWidth(20)
	win.Connect("destroy", func() { gtk.MainQuit() })

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 12)

	const labelChars = 22

	// ── WE path ───────────────────────────────────────────────────────────────
	pathBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	pathLabel, _ := gtk.LabelNew("Wallpaper Engine folder:")
	pathLabel.SetXAlign(0)
	pathLabel.SetWidthChars(labelChars)

	entry, _ := gtk.EntryNew()
	entry.SetHExpand(true)
	if cfg.WEPath != "" {
		entry.SetText(cfg.WEPath)
	} else {
		entry.SetPlaceholderText("not detected")
	}

	detectBtn, _ := gtk.ButtonNewWithLabel("Auto-detect")
	detectBtn.Connect("clicked", func() {
		if p := core.AutoDetectWEPath(); p != "" {
			entry.SetText(p)
		} else {
			entry.SetText("")
			entry.SetPlaceholderText("not found")
		}
	})

	pathBox.PackStart(pathLabel, false, false, 0)
	pathBox.PackStart(entry, true, true, 0)
	pathBox.PackStart(detectBtn, false, false, 0)

	// ── FPS (Target framerate) ────────────────────────────────────────────────
	fpsBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	fpsLabel, _ := gtk.LabelNew("Target framerate (FPS):")
	fpsLabel.SetXAlign(0)
	fpsLabel.SetWidthChars(labelChars)

	fpsAdj, _ := gtk.AdjustmentNew(float64(cfg.FPS), 1, 1000, 1, 10, 0)
	if cfg.FPS == 0 {
		fpsAdj.SetValue(30)
	}
	fpsSpin, _ := gtk.SpinButtonNew(fpsAdj, 1, 0)
	fpsSpin.SetHExpand(true)

	fpsBox.PackStart(fpsLabel, false, false, 0)
	fpsBox.PackStart(fpsSpin, true, true, 0)

	// ── Steam Web API key ─────────────────────────────────────────────────────
	apiBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	apiLabel, _ := gtk.LabelNew("Steam Web API key:")
	apiLabel.SetXAlign(0)
	apiLabel.SetWidthChars(labelChars)

	apiEntry, _ := gtk.EntryNew()
	apiEntry.SetHExpand(true)
	apiEntry.SetText(cfg.SteamAPIKey)
	apiEntry.SetPlaceholderText("Paste your API key here")
	// Hidden by default, with an eye icon to toggle reveal.
	apiEntry.SetVisibility(false)
	apiEntry.SetIconFromIconName(gtk.ENTRY_ICON_SECONDARY, "view-reveal-symbolic")
	apiEntry.SetIconTooltipText(gtk.ENTRY_ICON_SECONDARY, "Show / hide key")
	apiEntry.Connect("icon-press", func() {
		reveal := !apiEntry.GetVisibility()
		apiEntry.SetVisibility(reveal)
		if reveal {
			apiEntry.SetIconFromIconName(gtk.ENTRY_ICON_SECONDARY, "view-conceal-symbolic")
		} else {
			apiEntry.SetIconFromIconName(gtk.ENTRY_ICON_SECONDARY, "view-reveal-symbolic")
		}
	})

	// Hint marker beside the field: opens Steam's API-key page in the browser. A
	// "?" text label is used rather than a themed icon name so it renders in any
	// icon theme.
	const apiKeyURL = "https://steamcommunity.com/dev/apikey"
	apiHint, _ := gtk.ButtonNewWithLabel("?")
	apiHint.SetRelief(gtk.RELIEF_NONE)
	apiHint.SetTooltipText("Get a Steam Web API key (opens " + apiKeyURL + ")")
	apiHint.Connect("clicked", func() {
		exec.Command("xdg-open", apiKeyURL).Start() //nolint
	})

	apiBox.PackStart(apiLabel, false, false, 0)
	apiBox.PackStart(apiEntry, true, true, 0)
	apiBox.PackStart(apiHint, false, false, 0)

	// ── Theme (GUI skin) ──────────────────────────────────────────────────────
	themeBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	themeLabel, _ := gtk.LabelNew("Interface theme:")
	themeLabel.SetXAlign(0)
	themeLabel.SetWidthChars(labelChars)

	skinCombo, _ := gtk.ComboBoxTextNew()
	for _, s := range guiSkins {
		skinCombo.Append(s.Value, s.Label)
	}
	skin := cfg.GuiSkin
	if skin == "" {
		skin = "skindark"
	}
	skinCombo.SetActiveID(skin)

	themeBox.PackStart(themeLabel, false, false, 0)
	themeBox.PackStart(skinCombo, true, true, 0)

	// ── Audio source (visualizer capture) ─────────────────────────────────────
	// "Default (auto)" = LWE follows the default output sink's monitor. Otherwise
	// the chosen monitor source is forced via LWE_AUDIO_DEVICE.
	audioBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	audioLabel, _ := gtk.LabelNew("Audio source:")
	audioLabel.SetXAlign(0)
	audioLabel.SetWidthChars(labelChars)

	monCombo, _ := gtk.ComboBoxTextNew()
	monCombo.SetHExpand(true)
	present := map[string]bool{"": true}
	monCombo.Append("", "Default (auto)")
	// Output sinks (capture everything sent to that device).
	for _, m := range listAudioMonitors() {
		monCombo.Append(m, m)
		present[m] = true
	}
	// Individual applications (capture only that app's audio). Stored as "app:<name>".
	for _, a := range listAudioApps() {
		id := "app:" + a
		monCombo.Append(id, a+" (application)")
		present[id] = true
	}
	// Keep a previously-saved choice selectable even if it isn't present right now.
	if cfg.AudioDevice != "" && !present[cfg.AudioDevice] {
		label := cfg.AudioDevice + " (not detected)"
		if strings.HasPrefix(cfg.AudioDevice, "app:") {
			label = strings.TrimPrefix(cfg.AudioDevice, "app:") + " (application, not running)"
		}
		monCombo.Append(cfg.AudioDevice, label)
	}
	monCombo.SetActiveID(cfg.AudioDevice) // "" selects "Default (auto)"

	audioBox.PackStart(audioLabel, false, false, 0)
	audioBox.PackStart(monCombo, true, true, 0)

	// ── Preferred media player (now-playing) ──────────────────────────────────
	// "Any (default)" lets playerctl pick; otherwise prefer the chosen player and
	// fall back to any (value "<player>,%any"), forwarded to playerctl --player=.
	playerBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	playerLabel, _ := gtk.LabelNew("Preferred player:")
	playerLabel.SetXAlign(0)
	playerLabel.SetWidthChars(labelChars)

	playerCombo, _ := gtk.ComboBoxTextNew()
	playerCombo.SetHExpand(true)
	playerCombo.Append("", "Any (default)")
	players := listMediaPlayers()
	for _, p := range players {
		playerCombo.Append(p+",%any", p+" (then any)")
	}
	// Keep a previously-saved preference selectable even if its player isn't running.
	if cfg.MediaPlayer != "" {
		found := false
		for _, p := range players {
			if p+",%any" == cfg.MediaPlayer {
				found = true
				break
			}
		}
		if !found {
			// Same friendly label as the running-player entries: "<player>,%any"
			// → "<player> (then any)". Falls back to the raw value for anything
			// that isn't the standard prefer-then-any form.
			label := cfg.MediaPlayer
			if base, ok := strings.CutSuffix(cfg.MediaPlayer, ",%any"); ok {
				label = base + " (then any)"
			}
			playerCombo.Append(cfg.MediaPlayer, label)
		}
	}
	playerCombo.SetActiveID(cfg.MediaPlayer) // "" selects "Any (default)"

	playerBox.PackStart(playerLabel, false, false, 0)
	playerBox.PackStart(playerCombo, true, true, 0)

	// ── Now-playing as wallpaper text ─────────────────────────────────────────
	npCheck, _ := gtk.CheckButtonNewWithLabel("Show now-playing track as wallpaper text")
	npCheck.SetActive(cfg.NowPlayingText)
	npCheck.SetTooltipText(
		"Push the current track's title/artist (from playerctl/MPRIS) into web wallpapers " +
			"that show a header label, e.g. audio visualizers. Overrides their header text while playing.")

	// ── Custom wallpaper directories ──────────────────────────────────────────
	dirs := append([]string{}, cfg.CustomDirs...)

	dirsHeader, _ := gtk.LabelNew("")
	dirsHeader.SetMarkup("<b>Custom wallpaper directories</b>")
	dirsHeader.SetXAlign(0)

	dirsHint, _ := gtk.LabelNew("Extra directories to scan (manually-downloaded wallpapers).")
	dirsHint.SetXAlign(0)

	listBox, _ := gtk.ListBoxNew()
	scroll, _ := gtk.ScrolledWindowNew(nil, nil)
	scroll.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scroll.SetSizeRequest(-1, 120)
	scroll.Add(listBox)

	refresh := func() {
		for {
			row := listBox.GetRowAtIndex(0)
			if row == nil {
				break
			}
			listBox.Remove(row)
		}
		for _, d := range dirs {
			lbl, _ := gtk.LabelNew(d)
			lbl.SetXAlign(0)
			listBox.Add(lbl)
		}
		listBox.ShowAll()
	}
	refresh()

	dirBtnBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	addDirBtn, _ := gtk.ButtonNewWithLabel("Add…")
	removeDirBtn, _ := gtk.ButtonNewWithLabel("Remove")

	addDirBtn.Connect("clicked", func() {
		dlg, _ := gtk.FileChooserDialogNewWith2Buttons(
			"Choose a folder", win, gtk.FILE_CHOOSER_ACTION_SELECT_FOLDER,
			"Cancel", gtk.RESPONSE_CANCEL, "Add", gtk.RESPONSE_ACCEPT)
		if dlg.Run() == gtk.RESPONSE_ACCEPT {
			if d := dlg.GetFilename(); d != "" {
				dirs = append(dirs, d)
				refresh()
			}
		}
		dlg.Destroy()
	})

	removeDirBtn.Connect("clicked", func() {
		row := listBox.GetSelectedRow()
		if row == nil {
			return
		}
		i := row.GetIndex()
		if i >= 0 && i < len(dirs) {
			dirs = append(dirs[:i], dirs[i+1:]...)
			refresh()
		}
	})

	dirBtnBox.PackStart(addDirBtn, false, false, 0)
	dirBtnBox.PackStart(removeDirBtn, false, false, 0)

	// ── Status + actions ──────────────────────────────────────────────────────
	statusLabel, _ := gtk.LabelNew("")
	statusLabel.SetXAlign(0)

	btnBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	btnBox.SetHAlign(gtk.ALIGN_END)

	cancelBtn, _ := gtk.ButtonNewWithLabel("Cancel")
	cancelBtn.Connect("clicked", func() { gtk.MainQuit() })

	saveBtn, _ := gtk.ButtonNewWithLabel("Save")
	saveBtn.Connect("clicked", func() {
		p, _ := entry.GetText()
		cfg.WEPath = p
		apiKey, _ := apiEntry.GetText()
		cfg.SteamAPIKey = apiKey
		cfg.GuiSkin = skinCombo.GetActiveID()
		cfg.AudioDevice = monCombo.GetActiveID()
		cfg.MediaPlayer = playerCombo.GetActiveID()
		cfg.NowPlayingText = npCheck.GetActive()
		cfg.FPS = int(fpsSpin.GetValueAsInt())
		cfg.CustomDirs = dirs

		if err := core.SaveConfig(cfg); err != nil {
			statusLabel.SetMarkup("<span foreground='red'>Save error</span>")
		} else {
			// Apply immediately if a daemon is running (best-effort: it may not be).
			_ = core.ReloadDaemon()
			gtk.MainQuit()
		}
	})

	btnBox.PackStart(cancelBtn, false, false, 0)
	btnBox.PackStart(saveBtn, false, false, 0)

	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)

	box.PackStart(pathBox, false, false, 0)
	box.PackStart(apiBox, false, false, 0)
	box.PackStart(themeBox, false, false, 0)
	box.PackStart(fpsBox, false, false, 0)
	box.PackStart(audioBox, false, false, 0)
	box.PackStart(playerBox, false, false, 0)
	box.PackStart(npCheck, false, false, 0)
	box.PackStart(sep, false, false, 4)
	box.PackStart(dirsHeader, false, false, 0)
	box.PackStart(dirsHint, false, false, 0)
	box.PackStart(scroll, true, true, 0)
	box.PackStart(dirBtnBox, false, false, 0)
	box.PackStart(statusLabel, false, false, 0)
	box.PackStart(btnBox, false, false, 0)

	win.Add(box)
	win.ShowAll()
	gtk.Main()
}
