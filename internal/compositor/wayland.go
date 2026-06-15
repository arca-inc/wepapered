package compositor

/*
#cgo pkg-config: wayland-client
#include <wayland-client.h>
#include <stdlib.h>
#include <string.h>

struct my_output {
    struct wl_output *wl_output;
    int x, y;
    int width, height;
    int scale;
    char *name;
    struct my_output *next;
};

static struct my_output *output_list = NULL;

static void output_geometry(void *data, struct wl_output *wl_output, int32_t x, int32_t y,
                            int32_t physical_width, int32_t physical_height, int32_t subpixel,
                            const char *make, const char *model, int32_t transform) {
    struct my_output *o = data;
    o->x = x;
    o->y = y;
}

static void output_mode(void *data, struct wl_output *wl_output, uint32_t flags,
                        int32_t width, int32_t height, int32_t refresh) {
    struct my_output *o = data;
    if (flags & WL_OUTPUT_MODE_CURRENT) {
        o->width = width;
        o->height = height;
    }
}

static void output_done(void *data, struct wl_output *wl_output) {}

static void output_scale(void *data, struct wl_output *wl_output, int32_t factor) {
    struct my_output *o = data;
    o->scale = factor;
}

static void output_name(void *data, struct wl_output *wl_output, const char *name) {
    struct my_output *o = data;
    if (o->name) free(o->name);
    o->name = strdup(name);
}

static void output_description(void *data, struct wl_output *wl_output, const char *description) {}

static const struct wl_output_listener output_listener = {
    output_geometry,
    output_mode,
    output_done,
    output_scale,
    output_name,
    output_description
};

static void registry_global(void *data, struct wl_registry *wl_registry,
                            uint32_t name, const char *interface, uint32_t version) {
    if (strcmp(interface, wl_output_interface.name) == 0) {
        // We need at least version 4 for the output_name event.
        uint32_t bind_version = version >= 4 ? 4 : version;
        struct wl_output *wl_output = wl_registry_bind(wl_registry, name, &wl_output_interface, bind_version);
        
        struct my_output *o = calloc(1, sizeof(struct my_output));
        o->wl_output = wl_output;
        o->scale = 1; // default
        o->next = output_list;
        output_list = o;
        
        wl_output_add_listener(wl_output, &output_listener, o);
    }
}

static void registry_global_remove(void *data, struct wl_registry *wl_registry, uint32_t name) {}

static const struct wl_registry_listener registry_listener = {
    registry_global,
    registry_global_remove
};

static void fetch_wayland_outputs() {
    // free existing list if any
    struct my_output *curr = output_list;
    while (curr) {
        struct my_output *next = curr->next;
        if (curr->name) free(curr->name);
        if (curr->wl_output) wl_output_destroy(curr->wl_output);
        free(curr);
        curr = next;
    }
    output_list = NULL;

    struct wl_display *display = wl_display_connect(NULL);
    if (!display) return;

    struct wl_registry *registry = wl_display_get_registry(display);
    wl_registry_add_listener(registry, &registry_listener, NULL);

    wl_display_roundtrip(display); // get globals
    wl_display_roundtrip(display); // get output events

    wl_registry_destroy(registry);
    wl_display_disconnect(display);
}

static struct my_output *get_first_output() {
    return output_list;
}
*/
import "C"
import (
	"fmt"
	"sort"
)

// Wayland natively enumerates monitors via the wl_output protocol.
type Wayland struct{}

func (w *Wayland) Name() string { return "wayland-native" }

func (w *Wayland) EnvOverrides() map[string]string {
	return nil
}

func (w *Wayland) Outputs() ([]Output, error) {
	C.fetch_wayland_outputs()

	var raw []Output

	curr := C.get_first_output()
	for curr != nil {
		name := ""
		if curr.name != nil {
			name = C.GoString(curr.name)
		} else {
			name = "Unknown"
		}

		raw = append(raw, Output{
			Name:   name,
			X:      int(curr.x),
			Y:      int(curr.y),
			// In Wayland layout pixels, width/height is usually physical / scale
			Width:  int(curr.width) / int(curr.scale),
			Height: int(curr.height) / int(curr.scale),
		})
		curr = curr.next
	}

	if len(raw) == 0 {
		return nil, fmt.Errorf("no wayland outputs found or failed to connect to display")
	}

	sort.Slice(raw, func(i, j int) bool {
		if raw[i].X != raw[j].X {
			return raw[i].X < raw[j].X
		}
		return raw[i].Y < raw[j].Y
	})

	return raw, nil
}
