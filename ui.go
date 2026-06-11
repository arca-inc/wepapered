package main

import (
	"github.com/gotk3/gotk3/gtk"
)

func runConfigUI(cfg *Config) {
	gtk.Init(nil)

	win, _ := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	win.SetTitle("wepapered — config")
	win.SetDefaultSize(480, 200)
	win.SetResizable(false)
	win.SetBorderWidth(20)
	win.Connect("destroy", func() { gtk.MainQuit() })

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 12)

	// WE path row
	pathBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	label, _ := gtk.LabelNew("Wallpaper Engine path:")
	label.SetXAlign(0)
	label.SetWidthChars(24)

	entry, _ := gtk.EntryNew()
	entry.SetHExpand(true)
	if cfg.WEPath != "" {
		entry.SetText(cfg.WEPath)
	} else {
		entry.SetPlaceholderText("non détecté")
	}

	detectBtn, _ := gtk.ButtonNewWithLabel("Auto-détecter")
	detectBtn.Connect("clicked", func() {
		p := autoDetectWEPath()
		if p != "" {
			entry.SetText(p)
		} else {
			entry.SetText("")
			entry.SetPlaceholderText("introuvable")
		}
	})

	pathBox.PackStart(label, false, false, 0)
	pathBox.PackStart(entry, true, true, 0)
	pathBox.PackStart(detectBtn, false, false, 0)

	// API Key row
	apiBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	apiLabel, _ := gtk.LabelNew("Clé d'API Steam (Web):")
	apiLabel.SetXAlign(0)
	apiLabel.SetWidthChars(24)

	apiEntry, _ := gtk.EntryNew()
	apiEntry.SetHExpand(true)
	apiEntry.SetText(cfg.SteamAPIKey)
	apiEntry.SetPlaceholderText("Colle ta clé API ici")

	apiBox.PackStart(apiLabel, false, false, 0)
	apiBox.PackStart(apiEntry, true, true, 0)

	// Status label
	statusLabel, _ := gtk.LabelNew("")
	statusLabel.SetXAlign(0)

	// Buttons row
	btnBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	btnBox.SetHAlign(gtk.ALIGN_END)

	cancelBtn, _ := gtk.ButtonNewWithLabel("Annuler")
	cancelBtn.Connect("clicked", func() { gtk.MainQuit() })

	saveBtn, _ := gtk.ButtonNewWithLabel("Enregistrer")
	saveBtn.Connect("clicked", func() {
		p, _ := entry.GetText()
		cfg.WEPath = p
		apiKey, _ := apiEntry.GetText()
		cfg.SteamAPIKey = apiKey

		if err := saveConfig(cfg); err != nil {
			statusLabel.SetMarkup("<span foreground='red'>Erreur de sauvegarde</span>")
		} else {
			gtk.MainQuit()
		}
	})

	btnBox.PackStart(cancelBtn, false, false, 0)
	btnBox.PackStart(saveBtn, false, false, 0)

	box.PackStart(pathBox, false, false, 0)
	box.PackStart(apiBox, false, false, 0)
	box.PackStart(statusLabel, false, false, 0)
	box.PackStart(btnBox, false, false, 0)

	win.Add(box)
	win.ShowAll()
	gtk.Main()
}
