// wepapered-gui — lightweight WebKitGTK window for the WE browse UI.
// Feels like an Electron app: no address bar, no tabs, just the content. Built
// directly on webkit2gtk-4.1 + GTK3; links no LWE/GTK-settings code, only webkit
// + internal/core.
//
// The browse UI is served by the daemon on a random local port, discovered via the
// daemon's Unix control socket (see internal/core). This window does not start the
// daemon: it shows the UI when the daemon is reachable and a "can't reach the
// daemon" screen (hinting `wepaperedctl daemon`) when it isn't, polling every
// couple of seconds so it reconnects automatically and falls back if the daemon
// goes away (re-querying the port, which may change across daemon restarts).

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// Implemented in Go (bridge.go): daemon reachability + current UI URL, both via
// the daemon's Unix control socket.
extern int wepDaemonUp(void);
extern char *wepDaemonURL(void);

static void on_destroy(GtkWidget *w, gpointer d) { gtk_main_quit(); }

static void load_changed(WebKitWebView *wv, WebKitLoadEvent ev, gpointer d) {
    if (ev == WEBKIT_LOAD_FINISHED) {
        gtk_window_present(GTK_WINDOW(d));
    }
}

// Suppress WebKit's native context menu — WE draws its own on the DOM
// contextmenu event, which still fires. Returning TRUE cancels the default menu.
static gboolean on_context_menu(WebKitWebView *wv, WebKitContextMenu *menu,
                                GdkEvent *event, WebKitHitTestResult *hit, gpointer d) {
    return TRUE;
}

static void on_script_message(WebKitUserContentManager *m, WebKitJavascriptResult *r, gpointer p) {
    GtkWindow *win = GTK_WINDOW(p);
    JSCValue *value = webkit_javascript_result_get_js_value(r);
    char *str = jsc_value_to_string(value);

    if (g_strcmp0(str, "minimize") == 0) {
        gtk_window_iconify(win);
    } else if (g_strcmp0(str, "maximize") == 0) {
        if (gtk_window_is_maximized(win)) {
            gtk_window_unmaximize(win);
        } else {
            gtk_window_maximize(win);
        }
    } else if (g_strcmp0(str, "drag") == 0) {
        GdkDisplay *display = gtk_widget_get_display(GTK_WIDGET(win));
        GdkSeat *seat = gdk_display_get_default_seat(display);
        GdkDevice *pointer = gdk_seat_get_pointer(seat);
        int x, y;
        gdk_device_get_position(pointer, NULL, &x, &y);
        gtk_window_begin_move_drag(win, 1, x, y, GDK_CURRENT_TIME);
    } else if (g_strcmp0(str, "close") == 0) {
        gtk_window_close(win);
    }

    g_free(str);
}

// Shown when the daemon is unreachable.
static const char *DAEMON_DOWN_HTML =
"<!DOCTYPE html><html><head><meta charset='utf-8'><style>"
"html,body{height:100%;margin:0}"
"body{display:flex;align-items:center;justify-content:center;background:#15151a;"
"color:#e8e8ef;font-family:system-ui,-apple-system,sans-serif;-webkit-user-select:none;cursor:default}"
".card{max-width:520px;text-align:center;padding:40px}"
".dot{width:13px;height:13px;border-radius:50%;background:#ff5252;display:inline-block;"
"box-shadow:0 0 14px #ff5252;animation:pulse 1.4s ease-in-out infinite}"
"@keyframes pulse{0%,100%{opacity:.35}50%{opacity:1}}"
"h1{font-size:22px;font-weight:600;margin:20px 0 8px}"
"p{color:#9a9aa8;line-height:1.55;margin:6px 0 24px}"
"code{display:inline-block;background:#0d0d11;border:1px solid #2a2a33;border-radius:9px;"
"padding:13px 20px;color:#7fd1ff;font-family:ui-monospace,Menlo,monospace;font-size:15px}"
".hint{margin-top:20px;font-size:13px;color:#63636f}"
"</style></head><body><div class='card'>"
"<span class='dot'></span>"
"<h1>Can&rsquo;t reach the wepapered daemon</h1>"
"<p>The browse interface is served by the daemon, which doesn&rsquo;t appear to be running. Start it with:</p>"
"<code>wepaperedctl daemon & disown</code>"
"<div class='hint'>This window reconnects automatically once the daemon is up.</div>"
"</div></body></html>";

typedef struct {
    WebKitWebView *wv;
    char *override_url; // non-NULL: explicit URL (debug arg); else ask the daemon
    char *loaded_url;   // URL currently loaded; NULL = down screen showing
} AppCtx;

static void load_url(AppCtx *c, const char *u) {
    webkit_web_view_load_uri(c->wv, u);
    g_free(c->loaded_url);
    c->loaded_url = g_strdup(u);
}
static void show_down(AppCtx *c) {
    webkit_web_view_load_html(c->wv, DAEMON_DOWN_HTML, NULL);
    g_free(c->loaded_url);
    c->loaded_url = NULL;
}

// Polled from the GTK main loop. Recovery keys on the URL, not just up/down: the
// daemon binds a NEW random port on every start, so a restart changes the URL —
// even a restart that completes entirely between two polls, where the window never
// observes a "down" tick. So (re)load whenever the daemon's current URL differs
// from what's loaded, and drop to the down screen when it's unreachable.
static gboolean poll_daemon(gpointer data) {
    AppCtx *c = (AppCtx *)data;
    if (c->override_url) {
        if (c->loaded_url == NULL) load_url(c, c->override_url);
        return G_SOURCE_CONTINUE;
    }
    char *u = wepDaemonURL(); // "" if the daemon is unreachable
    if (u && u[0]) {
        if (c->loaded_url == NULL || g_strcmp0(c->loaded_url, u) != 0) load_url(c, u);
    } else if (c->loaded_url != NULL) {
        show_down(c);
    }
    if (u) free(u);
    return G_SOURCE_CONTINUE;
}

// On a failed navigation (e.g. the daemon restarted on a new port and refuses the
// old URL) drop to the down screen so the next poll re-queries the port and
// reloads — recovers at once instead of waiting to catch a "down" tick.
static gboolean on_load_failed(WebKitWebView *wv, WebKitLoadEvent ev, gchar *uri,
                               GError *err, gpointer d) {
    AppCtx *c = (AppCtx *)d;
    if (c->override_url) return FALSE; // debug override: let WebKit show the error
    show_down(c);
    return TRUE; // suppress WebKit's default error page
}

void run_ui_window(const char *override_url, int width, int height) {
    // Force X11/XWayland backend — WebKitWebProcess crashes on raw Wayland
    // sockets due to a protocol error (EPROTO/71) with webkit2gtk-4.1.
    g_setenv("GDK_BACKEND", "x11", TRUE);

    gtk_init(0, NULL);

    GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(win), "Wallpaper Engine");
    gtk_window_set_default_size(GTK_WINDOW(win), width, height);
    gtk_window_set_position(GTK_WINDOW(win), GTK_WIN_POS_CENTER);

    // Don't let the window shrink below a usable size (16:9 minimum).
    GdkGeometry minHints;
    minHints.min_width = 768;
    minHints.min_height = 432;
    gtk_window_set_geometry_hints(GTK_WINDOW(win), NULL, &minHints, GDK_HINT_MIN_SIZE);

    // No native title bar decorations — the WE UI draws its own top bar.
    gtk_window_set_decorated(GTK_WINDOW(win), FALSE);

    WebKitUserContentManager *ucm = webkit_user_content_manager_new();
    webkit_user_content_manager_register_script_message_handler(ucm, "host");

    WebKitWebView *wv = WEBKIT_WEB_VIEW(webkit_web_view_new_with_user_content_manager(ucm));
    g_signal_connect(ucm, "script-message-received::host", G_CALLBACK(on_script_message), win);

    WebKitSettings *settings = webkit_web_view_get_settings(wv);
    webkit_settings_set_allow_file_access_from_file_urls(settings, TRUE);
    webkit_settings_set_allow_universal_access_from_file_urls(settings, TRUE);
    webkit_settings_set_javascript_can_access_clipboard(settings, TRUE);
    webkit_settings_set_enable_developer_extras(settings, FALSE);
    // Disable GPU/GBM hardware acceleration — avoids "Failed to create GBM
    // buffer" errors when running under XWayland without DRM access.
    webkit_settings_set_hardware_acceleration_policy(
        settings, WEBKIT_HARDWARE_ACCELERATION_POLICY_NEVER);

    gtk_container_add(GTK_CONTAINER(win), GTK_WIDGET(wv));

    g_signal_connect(win, "destroy", G_CALLBACK(on_destroy), NULL);
    g_signal_connect(wv, "load-changed", G_CALLBACK(load_changed), win);
    g_signal_connect(wv, "context-menu", G_CALLBACK(on_context_menu), NULL);

    AppCtx *ctx = g_new0(AppCtx, 1);
    ctx->wv = wv;
    ctx->override_url = (override_url && override_url[0]) ? g_strdup(override_url) : NULL;
    g_signal_connect(wv, "load-failed", G_CALLBACK(on_load_failed), ctx);

    // Show the down screen, then immediately poll (loads the UI at once if the
    // daemon is already up) and keep polling to track restarts / port changes.
    show_down(ctx);
    poll_daemon(ctx);
    g_timeout_add_seconds(2, poll_daemon, ctx);

    gtk_widget_show_all(win);
    gtk_main();
}
*/
import "C"

import (
	"os"
	"unsafe"
)

func main() {
	// Force XWayland/X11 backend before GTK or WebKit touch the environment.
	// WebKitWebProcess is a subprocess that inherits our env — if WAYLAND_DISPLAY
	// is set and GDK_BACKEND isn't forced, the web process crashes with EPROTO/71.
	os.Setenv("GDK_BACKEND", "x11")
	os.Unsetenv("WAYLAND_DISPLAY")
	os.Setenv("WEBKIT_DISABLE_COMPOSITING_MODE", "1")

	// No URL normally — the window asks the daemon for its (random) port via the
	// control socket. An explicit arg overrides it, for debugging.
	override := ""
	if len(os.Args) > 1 {
		override = os.Args[1]
	}

	curl := C.CString(override)
	defer C.free(unsafe.Pointer(curl))

	C.run_ui_window(curl, 1280, 780)
}
