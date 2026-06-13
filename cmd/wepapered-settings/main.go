// wepapered-settings — the GTK3 configuration window. Edits the shared config
// (WE path, Steam API key, UI theme, loading backend, custom dirs). Links gotk3
// only; no LWE, no webkit.
package main

import (
	"github.com/gotk3/gotk3/gtk"

	"wepapered/internal/core"
)

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

	apiBox.PackStart(apiLabel, false, false, 0)
	apiBox.PackStart(apiEntry, true, true, 0)

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

	// ── Placeholder (loading) backend ─────────────────────────────────────────
	phBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	phLabel, _ := gtk.LabelNew("Loading backend:")
	phLabel.SetXAlign(0)
	phLabel.SetWidthChars(labelChars)

	phEntry, _ := gtk.EntryNew()
	phEntry.SetHExpand(true)
	phEntry.SetText(cfg.PlaceholderBackend)
	phEntry.SetPlaceholderText("hyprpaper (default)")

	phBox.PackStart(phLabel, false, false, 0)
	phBox.PackStart(phEntry, true, true, 0)

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
		phText, _ := phEntry.GetText()
		cfg.PlaceholderBackend = phText
		cfg.CustomDirs = dirs

		if err := core.SaveConfig(cfg); err != nil {
			statusLabel.SetMarkup("<span foreground='red'>Save error</span>")
		} else {
			gtk.MainQuit()
		}
	})

	btnBox.PackStart(cancelBtn, false, false, 0)
	btnBox.PackStart(saveBtn, false, false, 0)

	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)

	box.PackStart(pathBox, false, false, 0)
	box.PackStart(apiBox, false, false, 0)
	box.PackStart(themeBox, false, false, 0)
	box.PackStart(phBox, false, false, 0)
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
