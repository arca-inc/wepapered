// wepapered-gui — lightweight WebKitGTK window for the WE browse UI.
// Feels like an Electron app: no address bar, no tabs, just the content. Built
// directly on webkit2gtk-4.1 + GTK3; links no LWE/GTK-settings code, only webkit
// + internal/core.
//
// The browse UI is served by the daemon (127.0.0.1:9001). This window does not
// start the daemon: it shows the UI when the daemon is reachable and a "can't
// reach the daemon" screen (hinting `wepaperedctl daemon`) when it isn't, polling
// every couple of seconds so it reconnects automatically and falls back if the
// daemon goes away.

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>

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

// Shown when the daemon's control port (127.0.0.1:9001) is unreachable.
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
"<code>wepaperedctl daemon</code>"
"<div class='hint'>This window reconnects automatically once the daemon is up.</div>"
"</div></body></html>";

// daemon_reachable reports whether something is listening on 127.0.0.1:9001.
// A connect() to loopback returns immediately (success or ECONNREFUSED), so this
// is safe to call from the GTK main loop.
static gboolean daemon_reachable(void) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd < 0) return FALSE;
    struct sockaddr_in a;
    memset(&a, 0, sizeof(a));
    a.sin_family = AF_INET;
    a.sin_port = htons(9001);
    inet_pton(AF_INET, "127.0.0.1", &a.sin_addr);
    int r = connect(fd, (struct sockaddr *)&a, sizeof(a));
    close(fd);
    return r == 0 ? TRUE : FALSE;
}

typedef struct {
    WebKitWebView *wv;
    char *url;
    int showing_ui; // 1 = browse UI loaded, 0 = down screen loaded
} AppCtx;

static void show_ui(AppCtx *c)   { webkit_web_view_load_uri(c->wv, c->url); c->showing_ui = 1; }
static void show_down(AppCtx *c) { webkit_web_view_load_html(c->wv, DAEMON_DOWN_HTML, NULL); c->showing_ui = 0; }

// Polled from the GTK main loop: swap between the UI and the down screen only on
// a state change, so the UI is not reloaded on every tick.
static gboolean poll_daemon(gpointer data) {
    AppCtx *c = (AppCtx *)data;
    gboolean up = daemon_reachable();
    if (up && !c->showing_ui)      show_ui(c);
    else if (!up && c->showing_ui) show_down(c);
    return G_SOURCE_CONTINUE;
}

void run_ui_window(const char *url, int width, int height) {
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
    ctx->url = g_strdup(url);

    // Initial content from the current reachability, then poll to keep in sync.
    if (daemon_reachable()) show_ui(ctx); else show_down(ctx);
    g_timeout_add_seconds(2, poll_daemon, ctx);

    gtk_widget_show_all(win);
    gtk_main();
}
*/
import "C"
import (
	"os"
	"unsafe"

	"wepapered/internal/core"
)

func main() {
	// Force XWayland/X11 backend before GTK or WebKit touch the environment.
	// WebKitWebProcess is a subprocess that inherits our env — if WAYLAND_DISPLAY
	// is set and GDK_BACKEND isn't forced, the web process crashes with EPROTO/71.
	os.Setenv("GDK_BACKEND", "x11")
	os.Unsetenv("WAYLAND_DISPLAY")
	os.Setenv("WEBKIT_DISABLE_COMPOSITING_MODE", "1")

	cfg, _ := core.LoadConfig()
	url := core.GUIURL(cfg)
	if len(os.Args) > 1 {
		url = os.Args[1] // explicit URL override, for debugging
	}

	curl := C.CString(url)
	defer C.free(unsafe.Pointer(curl))

	C.run_ui_window(curl, 1280, 780)
}
