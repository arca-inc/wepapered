// wepapered-gui — lightweight WebKitGTK window for the WE browse UI.
// Feels like an Electron app: no address bar, no tabs, just the content. Built
// directly on webkit2gtk-4.1 + GTK3; ensures the daemon is up, then opens the
// browse UI it serves. Links no LWE/GTK-settings code, only webkit + internal/core.

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>

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

void run_ui_window(const char *url, int width, int height) {
    // Force X11/XWayland backend — WebKitWebProcess crashes on raw Wayland
    // sockets due to a protocol error (EPROTO/71) with webkit2gtk-4.1.
    g_setenv("GDK_BACKEND", "x11", TRUE);

    gtk_init(0, NULL);

    GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(win), "Wallpaper Engine");
    gtk_window_set_default_size(GTK_WINDOW(win), width, height);
    gtk_window_set_position(GTK_WINDOW(win), GTK_WIN_POS_CENTER);

    // No native title bar decorations — the WE UI draws its own top bar
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

    webkit_web_view_load_uri(wv, url);

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

	// The window is useless without the daemon's WS server — ensure it's running.
	ensureDaemon()

	cfg, _ := core.LoadConfig()
	url := core.GUIURL(cfg)
	if len(os.Args) > 1 {
		url = os.Args[1] // explicit URL override, for debugging
	}

	curl := C.CString(url)
	defer C.free(unsafe.Pointer(curl))

	C.run_ui_window(curl, 1280, 780)
}
