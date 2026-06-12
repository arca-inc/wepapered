// wepapered-ui — lightweight WebKitGTK window for the WE browse UI.
// Feels like an Electron app: no address bar, no tabs, just the content.
// Built directly on webkit2gtk-4.1 + GTK3, zero extra Go dependencies.

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>

static void on_destroy(GtkWidget *w, gpointer d) { gtk_main_quit(); }

static void load_changed(WebKitWebView *wv, WebKitLoadEvent ev, gpointer d) {
    if (ev == WEBKIT_LOAD_FINISHED) {
        // Ensure the window gets focus after page load
        gtk_window_present(GTK_WINDOW(d));
    }
}

void run_ui_window(const char *url, int width, int height) {
    gtk_init(0, NULL);

    GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(win), "Wallpaper Engine");
    gtk_window_set_default_size(GTK_WINDOW(win), width, height);
    gtk_window_set_position(GTK_WINDOW(win), GTK_WIN_POS_CENTER);

    // No native title bar decorations — the WE UI draws its own top bar
    gtk_window_set_decorated(GTK_WINDOW(win), FALSE);

    WebKitWebView *wv = WEBKIT_WEB_VIEW(webkit_web_view_new());

    // Allow running local files and cross-origin requests to localhost
    WebKitSettings *settings = webkit_web_view_get_settings(wv);
    webkit_settings_set_allow_file_access_from_file_urls(settings, TRUE);
    webkit_settings_set_allow_universal_access_from_file_urls(settings, TRUE);
    webkit_settings_set_javascript_can_access_clipboard(settings, TRUE);
    webkit_settings_set_enable_developer_extras(settings, FALSE);
    // Disable context menu for app-like feel
    webkit_settings_set_enable_caret_browsing(settings, FALSE);

    gtk_container_add(GTK_CONTAINER(win), GTK_WIDGET(wv));

    g_signal_connect(win, "destroy", G_CALLBACK(on_destroy), NULL);
    g_signal_connect(wv, "load-changed", G_CALLBACK(load_changed), win);

    webkit_web_view_load_uri(wv, url);

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
	url := "http://localhost:9001/ui/index.html?skinStyle=styles/main.css&skinKey=default#/browsewallpapers"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}

	curl := C.CString(url)
	defer C.free(unsafe.Pointer(curl))

	C.run_ui_window(curl, 1280, 780)
}
