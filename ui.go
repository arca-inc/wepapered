package main

import (
	"os"

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

	// Status label
	statusLabel, _ := gtk.LabelNew("")
	statusLabel.SetXAlign(0)

	// Buttons row
	btnBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	btnBox.SetHAlign(gtk.ALIGN_END)

	cancelBtn, _ := gtk.ButtonNewWithLabel("Annuler")
	cancelBtn.Connect("clicked", func() { gtk.MainQuit() })

	saveBtn, _ := gtk.ButtonNewWithLabel("Sauvegarder")
	if sc, err := saveBtn.GetStyleContext(); err == nil {
		sc.AddClass("suggested-action")
	}
	saveBtn.Connect("clicked", func() {
		text, _ := entry.GetText()
		if text != "" {
			if _, err := os.Stat(text); err != nil {
				statusLabel.SetText("⚠ chemin introuvable")
				return
			}
		}
		cfg.WEPath = text
		if err := saveConfig(cfg); err != nil {
			statusLabel.SetText("erreur sauvegarde: " + err.Error())
			return
		}
		statusLabel.SetText("✓ sauvegardé")
	})

	btnBox.PackEnd(saveBtn, false, false, 0)
	btnBox.PackEnd(cancelBtn, false, false, 0)

	box.PackStart(pathBox, false, false, 0)
	box.PackStart(statusLabel, false, false, 0)
	box.PackEnd(btnBox, false, false, 0)

	win.Add(box)
	win.ShowAll()
	gtk.Main()
}
