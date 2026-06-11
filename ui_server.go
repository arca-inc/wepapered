package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// serveUI serves the Wallpaper Engine UI over HTTP, injecting the JS bridge shim
// into the index.html so it can communicate with the Go daemon via WebSocket.
func (s *WSServer) serveUI(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WEPath == "" {
		http.Error(w, "WEPath not configured", http.StatusInternalServerError)
		return
	}

	uiDistPath := filepath.Join(s.cfg.WEPath, "ui", "dist")
	reqPath := strings.TrimPrefix(r.URL.Path, "/ui")
	if reqPath == "" || reqPath == "/" {
		reqPath = "/index.html"
	}

	filePath := filepath.Join(uiDistPath, reqPath)

	// If serving index.html, inject our bridge shim
	if reqPath == "/index.html" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			http.Error(w, "Failed to read index.html", http.StatusInternalServerError)
			return
		}

		// Filter out the old wepapered-inject.js if it's there
		content = bytes.Replace(content, []byte(`<script src="wepapered-inject.js"></script>`), []byte(""), -1)

	// Detect language from Accept-Language header
	lang := "en-us"
	if al := r.Header.Get("Accept-Language"); al != "" {
		lang = strings.ToLower(strings.Split(strings.Split(al, ",")[0], ";")[0])
	}
	locale := loadLocale(s.cfg.WEPath, lang)
	if len(locale) == 0 {
		locale = loadLocale(s.cfg.WEPath, "en-us")
	}
	localeJSON, _ := json.Marshal(locale)

	shim := `<script>
// wepapered bridge shim for hosted CEF UI

window.__wepLocale = ` + string(localeJSON) + `;
window.wepaperedBridge = new WebSocket('ws://' + location.host + '/we');
window.wepaperedQueue = [];

window.wepaperedBridge.onopen = function() {
	while (window.wepaperedQueue.length > 0) {
		window.wepaperedBridge.send(window.wepaperedQueue.shift());
	}
};

function sendToBridge(payload) {
	var data = JSON.stringify(payload);
	if (window.wepaperedBridge.readyState === 1) {
		window.wepaperedBridge.send(data);
	} else {
		window.wepaperedQueue.push(data);
	}
}

function updateUIState() {
	var val = window.browseWallpapersCtrl;
	var state = window.daemonState;
	if (!val || !state || !state.monitors) return;
	if (!val.applyMonitorConfigurationAndWallpaperConfig) return;
	
	var monitorsArray = [];
	var selectedWallpapers = {};
	var loc = 0;

	if (!val.steamWorkshopStatus) {
		val.steamWorkshopStatus = { error: false, complete: true, hidden: true };
	}

	var list = val.wallpapers || [];
	function findWp(file) {
		for (var i=0; i<list.length; i++) {
			if (list[i].file === file) return list[i];
		}
		return null;
	}

	if (val.getDisplayTags && !val.getDisplayTags.__wepPatched) {
		var origGetDisplayTags = val.getDisplayTags;
		val.getDisplayTags = function(e) {
			if (!e || typeof e !== 'string') return [];
			return origGetDisplayTags.call(this, e);
		};
		val.getDisplayTags.__wepPatched = true;
	}

	for (var key in state.monitors) {
		var m = state.monitors[key];
		var match = key.match(/Monitor(\d+)/);
		var idx = match ? parseInt(match[1]) : 0;
		var wp = findWp(m.win_path) || {
			file: m.win_path,
			type: m.type,
			workshopid: m.workshop_id,
			tags: "",
			title: key
		};
		
		monitorsArray.push({
			index: idx,
			location: idx,
			name: key,
			x0: idx * 1920,
			y0: 0,
			x1: (idx + 1) * 1920,
			y1: 1080
		});
		
		selectedWallpapers[idx] = wp;
		if (m.device_path) selectedWallpapers[m.device_path] = wp;
		selectedWallpapers[key] = wp;
		loc++;
	}
	
	val.applyMonitorConfigurationAndWallpaperConfig(
		monitorsArray,
		{ wallpaperconfig: { selectedwallpapers: selectedWallpapers, layout: 0 } },
		{},
		false
	);
	
	try { val.$apply(); } catch(e){}
}

var _browseWallpapersCtrl;
Object.defineProperty(window, 'browseWallpapersCtrl', {
	get: function() { return _browseWallpapersCtrl; },
	set: function(val) {
		_browseWallpapersCtrl = val;
		setTimeout(function() {
			if (val && window.__pendingLibrary) {
				if (val.setListSource) {
					try { val.setListSource('installed'); } catch(e){}
				}
				val.updateWallpapers(window.__pendingLibrary, []);
				val.sortWallpapers();
				window.__pendingLibrary = null;
			}
			updateUIState();
		}, 100);
	}
});

window.wepaperedBridge.onmessage = function(e) {
	var msg = JSON.parse(e.data);
	if (msg.type === 'state') {
		window.daemonState = msg.state;
		setTimeout(function() { updateUIState(); }, 100);
	} else if (msg.type === 'library') {
		if (window.browseWallpapersCtrl) {
			if (window.browseWallpapersCtrl.setListSource) {
				try { window.browseWallpapersCtrl.setListSource('installed'); } catch(e){}
			}
			window.browseWallpapersCtrl.updateWallpapers(msg.wallpapers, []);
			window.browseWallpapersCtrl.sortWallpapers();
			window.browseWallpapersCtrl.$apply();
		} else {
			window.__pendingLibrary = msg.wallpapers;
		}
	} else if (msg.callback && window[msg.callback]) {
		window[msg.callback].apply(null, msg.args || []);
	}
};



var BRIDGE_OBJECTS = ['browseWallpaperObject', 'settingsObject', 'welcomeObject', 'editorObject'];

var globalImpl = {
	_wpxLocale: function(lang) {
		return { success: true, translations: JSON.stringify(window.__wepLocale) };
	},
	jsInit: function() {
		if (window.onOSProviderConnectionChanged) {
			window.onOSProviderConnectionChanged(true);
		}
	}
};

var uiImpl = {
	getLanguage: function() { return navigator.language.toLowerCase() || 'en-us'; },
	getLocales: function() { return JSON.stringify(window.__wepLocale); },
	systemLog: function() {},
	openLink: function(url) {}
};

function makeProxy(impl, objectName) {
	return new Proxy(impl, {
		get: function(target, prop) {
			if (prop in target) return target[prop];
			if (typeof prop === 'symbol') return undefined;
			return function() {
				sendToBridge({
					object: objectName,
					method: String(prop),
					args: Array.prototype.slice.call(arguments)
				});
				return undefined;
			};
		}
	});
}

try { Object.defineProperty(window, 'global', { value: makeProxy(globalImpl, 'global'), writable: true, configurable: true }); } catch (e) { window.global = makeProxy(globalImpl, 'global'); }
try { Object.defineProperty(window, 'ui', { value: makeProxy(uiImpl, 'ui'), writable: true, configurable: true }); } catch (e) { window.ui = makeProxy(uiImpl, 'ui'); }

BRIDGE_OBJECTS.forEach(function(name) {
	try {
		Object.defineProperty(window, name, {
			value: new Proxy({}, {
				get: function(_, prop) {
					if (typeof prop === 'symbol') return undefined;
					return function() {
						var args = Array.prototype.slice.call(arguments);
						var cbName = name + '_' + prop + '_callback_' + Math.floor(Math.random()*10000);
						sendToBridge({
							object: name,
							method: prop,
							args: args,
							callback: cbName
						});
						return {
							then: function(cb) {
								window[cbName] = function() {
									delete window[cbName];
									if(cb) cb.apply(null, arguments);
								};
							}
						};
					};
				}
			}),
			writable: true,
			configurable: true
		});
	} catch(e){}
});

</script>`
		
		injected := bytes.Replace(content, []byte("<head>"), []byte("<head>"+shim), 1)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Content-Type", "text/html")
		w.Write(injected)
		return
	}

	http.ServeFile(w, r, filePath)
}
