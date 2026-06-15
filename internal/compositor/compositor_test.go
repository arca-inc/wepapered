package compositor

import (
	"errors"
	"testing"
)

func TestDetect(t *testing.T) {
	c, err := Detect()
	if err != nil {
		// No supported compositor in this environment (e.g. CI) — must be the
		// sentinel, and no compositor returned.
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Detect() error = %v; want ErrUnsupported", err)
		}
		if c != nil {
			t.Fatal("Detect() returned a compositor alongside an error")
		}
		return
	}
	if c == nil || c.Name() == "" {
		t.Fatal("Detect() returned no usable compositor and no error")
	}
}

func TestHyprlandIsCompositor(t *testing.T) {
	// Compile-time + behavior check that Hyprland satisfies the interface.
	var c Compositor = &Hyprland{}
	if c.Name() != "hyprland" {
		t.Fatalf("Name() = %q; want hyprland", c.Name())
	}
	// EnvOverrides must never panic and returns a (possibly empty/nil) map.
	_ = c.EnvOverrides()
}
