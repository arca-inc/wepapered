package daemon

// Persistent loading overlay, in-process. A dedicated GTK thread (started once at
// daemon startup) runs a gtk_main loop and owns one wlr-layer-shell window per output
// (via gtk-layer-shell). Showing/hiding is just gtk_widget_show/hide marshalled onto
// the GTK thread with g_idle_add — no process spawn, no GTK re-init, so the loading
// screen pops instantly. systray is D-Bus based, so this GTK loop doesn't conflict.

/*
#cgo pkg-config: gtk+-3.0 gtk-layer-shell-0
#cgo LDFLAGS: -lm
#include <gtk/gtk.h>
#include <gtk-layer-shell/gtk-layer-shell.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

#define WEP_MAX_OUT 16

typedef struct {
	char name[64];
	int  x, y;
	int  active;       // currently shown
	GtkWidget *win;
	GtkWidget *area;
} WepOv;

static WepOv   wep_ov[WEP_MAX_OUT];
static int     wep_ov_n = 0;
static GdkPixbuf *wep_logo = NULL;
static double  wep_phase = 0.0;

static void wep_rrect(cairo_t *cr, double x, double y, double w, double h, double r) {
	if (r > h/2.0) r = h/2.0;
	if (r > w/2.0) r = w/2.0;
	cairo_new_sub_path(cr);
	cairo_arc(cr, x + w - r, y + r,     r, -G_PI/2, 0);
	cairo_arc(cr, x + w - r, y + h - r, r, 0,        G_PI/2);
	cairo_arc(cr, x + r,     y + h - r, r, G_PI/2,   G_PI);
	cairo_arc(cr, x + r,     y + r,     r, G_PI,     3*G_PI/2);
	cairo_close_path(cr);
}

static gboolean wep_on_draw(GtkWidget *w, cairo_t *cr, gpointer data) {
	int W = gtk_widget_get_allocated_width(w);
	int H = gtk_widget_get_allocated_height(w);

	cairo_set_source_rgb(cr, 0.039, 0.039, 0.063);
	cairo_paint(cr);

	// Lay out the logo + loading bar as one block and centre it on the surface.
	double gap = 34.0;
	double barH = 6.0;
	double barW = fmin(360.0, W * 0.26);
	if (barW < 160) barW = 160;

	double dw = 0, dh = 0, scale = 1.0;
	if (wep_logo) {
		int lw = gdk_pixbuf_get_width(wep_logo);
		int lh = gdk_pixbuf_get_height(wep_logo);
		double target = fmin(256.0, H * 0.24);
		scale = target / (double)(lw > lh ? lw : lh);
		dw = lw * scale;
		dh = lh * scale;
	}

	double textGap = 30.0;
	double textH = 24.0; // ~22px caption
	double groupH = dh + (dh > 0 ? gap : 0) + barH + textGap + textH;
	double top = (H - groupH) / 2.0;

	if (wep_logo) {
		double lx = (W - dw) / 2.0;
		double ly = top;
		cairo_save(cr);
		cairo_translate(cr, lx, ly);
		cairo_scale(cr, scale, scale);
		gdk_cairo_set_source_pixbuf(cr, wep_logo, 0, 0);
		cairo_paint(cr);
		cairo_restore(cr);
	}

	double bx = (W - barW) / 2.0;
	double by = top + dh + (dh > 0 ? gap : 0);
	double r = barH / 2.0;

	cairo_set_source_rgba(cr, 1, 1, 1, 0.12);
	wep_rrect(cr, bx, by, barW, barH, r);
	cairo_fill(cr);

	double segW = barW * 0.34;
	double travel = barW + segW;
	double sx = bx - segW + wep_phase * travel;
	cairo_save(cr);
	wep_rrect(cr, bx, by, barW, barH, r);
	cairo_clip(cr);
	cairo_set_source_rgb(cr, 0.498, 0.820, 1.0);
	wep_rrect(cr, sx, by, segW, barH, r);
	cairo_fill(cr);
	cairo_restore(cr);

	// Caption below the bar.
	cairo_select_font_face(cr, "sans-serif", CAIRO_FONT_SLANT_NORMAL, CAIRO_FONT_WEIGHT_NORMAL);
	cairo_set_font_size(cr, 22.0);
	const char *caption = "By gamers, for gamers";
	cairo_text_extents_t ext;
	cairo_text_extents(cr, caption, &ext);
	cairo_set_source_rgba(cr, 1, 1, 1, 0.55);
	cairo_move_to(cr, (W - ext.width) / 2.0 - ext.x_bearing, by + barH + textGap + textH);
	cairo_show_text(cr, caption);

	return FALSE;
}

static gboolean wep_tick(gpointer _unused) {
	wep_phase += 0.012;
	if (wep_phase > 1.0) wep_phase -= 1.0;
	for (int i = 0; i < wep_ov_n; i++) {
		if (wep_ov[i].active && wep_ov[i].area) {
			gtk_widget_queue_draw(wep_ov[i].area);
		}
	}
	return G_SOURCE_CONTINUE;
}

// Find (or lazily create, hidden) the overlay window for an output name.
static WepOv *wep_ensure(const char *name, int x, int y) {
	for (int i = 0; i < wep_ov_n; i++) {
		if (strcmp(wep_ov[i].name, name) == 0) return &wep_ov[i];
	}
	if (wep_ov_n >= WEP_MAX_OUT) return NULL;
	WepOv *o = &wep_ov[wep_ov_n];
	memset(o, 0, sizeof(*o));
	strncpy(o->name, name, sizeof(o->name) - 1);
	o->x = x; o->y = y;

	GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	gtk_layer_init_for_window(GTK_WINDOW(win));
	gtk_layer_set_namespace(GTK_WINDOW(win), "wepapered-loading");
	gtk_layer_set_layer(GTK_WINDOW(win), GTK_LAYER_SHELL_LAYER_BOTTOM);

	GdkDisplay *disp = gdk_display_get_default();
	int n = gdk_display_get_n_monitors(disp);
	for (int i = 0; i < n; i++) {
		GdkMonitor *m = gdk_display_get_monitor(disp, i);
		GdkRectangle g;
		gdk_monitor_get_geometry(m, &g);
		if (g.x == x && g.y == y) { gtk_layer_set_monitor(GTK_WINDOW(win), m); break; }
	}
	gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_LEFT,   TRUE);
	gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_RIGHT,  TRUE);
	gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_TOP,    TRUE);
	gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_BOTTOM, TRUE);
	gtk_layer_set_exclusive_zone(GTK_WINDOW(win), -1);
	gtk_layer_set_keyboard_mode(GTK_WINDOW(win), GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);

	GtkWidget *area = gtk_drawing_area_new();
	gtk_container_add(GTK_CONTAINER(win), area);
	g_signal_connect(area, "draw", G_CALLBACK(wep_on_draw), NULL);

	o->win = win;
	o->area = area;
	wep_ov_n++;
	return o;
}

typedef struct { char name[64]; int x, y, show; } WepCmd;

static gboolean wep_apply(gpointer data) {
	WepCmd *c = (WepCmd *)data;
	WepOv *o = wep_ensure(c->name, c->x, c->y);
	if (o) {
		o->active = c->show;
		if (c->show) {
			gtk_widget_show_all(o->win);
			GdkWindow *gw = gtk_widget_get_window(o->win); // click-through
			if (gw) {
				cairo_region_t *e = cairo_region_create();
				gdk_window_input_shape_combine_region(gw, e, 0, 0);
				cairo_region_destroy(e);
			}
		} else {
			gtk_widget_hide(o->win);
		}
	}
	free(c);
	return G_SOURCE_REMOVE;
}

// wep_overlay_request marshals a show/hide onto the GTK thread (g_idle_add is
// thread-safe). Safe to call from any goroutine.
static void wep_overlay_request(const char *name, int x, int y, int show) {
	WepCmd *c = (WepCmd *)malloc(sizeof(WepCmd));
	memset(c, 0, sizeof(*c));
	strncpy(c->name, name, sizeof(c->name) - 1);
	c->x = x; c->y = y; c->show = show;
	g_idle_add(wep_apply, c);
}

// wep_overlay_run inits GTK and runs the main loop (blocks). Returns immediately if
// there's no display or the compositor lacks layer-shell, leaving the overlay disabled
// without aborting the daemon.
static void wep_overlay_run(const char *logoPath) {
	if (!gtk_init_check(0, NULL)) return;        // no display — don't abort the daemon
	if (!gtk_layer_is_supported()) return;       // not a wlr-layer-shell compositor
	GError *err = NULL;
	wep_logo = gdk_pixbuf_new_from_file(logoPath, &err);
	if (err) { g_error_free(err); wep_logo = NULL; }
	g_timeout_add(16, wep_tick, NULL);
	gtk_main();
}
*/
import "C"

import (
	"os"
	"runtime"
	"sync"
	"unsafe"

	"wepapered/assets"
)

var overlayOnce sync.Once

// startLoadingOverlay launches the persistent GTK overlay loop on its own OS thread.
// Idempotent; call once at daemon startup so the overlay is pre-initialised and shows
// instantly when needed.
func startLoadingOverlay() {
	overlayOnce.Do(func() {
		// Layer-shell requires the Wayland GDK backend.
		os.Setenv("GDK_BACKEND", "wayland")
		logoPath := "/tmp/wepapered-loading-logo.png"
		_ = os.WriteFile(logoPath, assets.LoadingPNG, 0o644)
		go func() {
			runtime.LockOSThread() // all GTK calls must stay on this thread
			cpath := C.CString(logoPath)
			defer C.free(unsafe.Pointer(cpath))
			C.wep_overlay_run(cpath) // blocks in gtk_main
		}()
	})
}

// overlayShow / overlayHide map an output to its persistent overlay window and toggle
// visibility on the GTK thread. Safe to call from any goroutine.
func overlayShow(name string, x, y int) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.wep_overlay_request(cname, C.int(x), C.int(y), 1)
}

func overlayHide(name string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.wep_overlay_request(cname, 0, 0, 0)
}
