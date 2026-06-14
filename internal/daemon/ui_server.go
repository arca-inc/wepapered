package daemon

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
	if s.cfg.Load().WEPath == "" {
		http.Error(w, "WEPath not configured", http.StatusInternalServerError)
		return
	}

	uiDistPath := filepath.Join(s.cfg.Load().WEPath, "ui", "dist")
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
	locale := loadLocale(s.cfg.Load().WEPath, lang)
	if len(locale) == 0 {
		locale = loadLocale(s.cfg.Load().WEPath, "en-us")
	}
	
	// Override the main title
	locale["ui_caption_browse"] = "Wepapered - Made for gamers"
	
	localeJSON, _ := json.Marshal(locale)

	shim := `<script>
// wepapered bridge shim for hosted CEF UI

window.__wepLocale = ` + string(localeJSON) + `;
window.wepaperedQueue = [];

// Reconnecting bridge: the daemon may restart (dev rebuilds, crashes) and the
// CEF UI window is long-lived, so reconnect instead of going dead until reload.
// The actual message handler is installed later as window.__bridgeOnMessage;
// every (re)connected socket dispatches through it, and the daemon re-pushes
// library/locale/state on each connect.
function connectBridge() {
	var ws = new WebSocket('ws://' + location.host + '/we');
	window.wepaperedBridge = ws;
	ws.onopen = function() {
		while (window.wepaperedQueue.length > 0) { ws.send(window.wepaperedQueue.shift()); }
	};
	ws.onmessage = function(e) { if (window.__bridgeOnMessage) window.__bridgeOnMessage(e); };
	ws.onclose = function() { setTimeout(connectBridge, 2000); };
	ws.onerror = function() { try { ws.close(); } catch (e) {} };
}
connectBridge();

function sendToBridge(payload) {
	var data = JSON.stringify(payload);
	if (window.wepaperedBridge && window.wepaperedBridge.readyState === 1) {
		window.wepaperedBridge.send(data);
	} else {
		window.wepaperedQueue.push(data);
	}
}

var origConsoleLog = console.log;
console.log = function() {
	var msg = Array.prototype.slice.call(arguments).map(function(arg) {
		return typeof arg === 'object' ? JSON.stringify(arg) : String(arg);
	}).join(' ');
	sendToBridge({type: 'log', msg: msg});
	origConsoleLog.apply(console, arguments);
};

function updateUIState() {
	var val = window.browseWallpapersCtrl;
	var state = window.daemonState;
	if (!val || !state || !state.monitors) return;
	if (!val.applyMonitorConfigurationAndWallpaperConfig) return;
	
	var monitorsArray = [];
	var selectedWallpapers = {};
	var loc = 0;

	// Restore persisted browser settings (e.g. "Show on start" /
	// showmonitorselectiononstart) BEFORE the monitor config is applied, since
	// that is when the UI decides whether to auto-open the monitor selection.
	if (val.browserSettings && state.browser_settings) {
		try { Object.assign(val.browserSettings, state.browser_settings); } catch (e) {}
	}

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

	// Build the "Choose display" list from the real displays the daemon pushed,
	// so the picker is populated even before any wallpaper is assigned. Without
	// this the list was derived from state.monitors (assigned wallpapers only),
	// which is empty on a fresh setup -> no display selectable -> nothing applies.
	var displays = window.daemonDisplays || [];
	if (!displays.length) {
		for (var sk in state.monitors) {
			var sm = sk.match(/Monitor(\d+)/);
			var sidx = sm ? parseInt(sm[1]) : 0;
			displays.push({ index: sidx, location: sidx, name: sk });
		}
	}
	for (var di = 0; di < displays.length; di++) {
		var d = displays[di];
		var didx = (typeof d.location === 'number') ? d.location : di;
		monitorsArray.push({
			index: didx,
			location: didx,
			name: d.name || ('Monitor' + didx),
			deviceName: d.deviceName || d.name || ('Monitor' + didx),
			devicePath: d.devicePath || ('Monitor' + didx),
			isClone: false,
			isInGroup: false,
			x0: (typeof d.x0 === 'number') ? d.x0 : didx * 1920,
			y0: (typeof d.y0 === 'number') ? d.y0 : 0,
			x1: (typeof d.x1 === 'number') ? d.x1 : (didx + 1) * 1920,
			y1: (typeof d.y1 === 'number') ? d.y1 : 1080
		});
	}

	// Overlay any wallpapers already assigned in daemon state onto those displays.
	for (var key in state.monitors) {
		var m = state.monitors[key];
		var match = key.match(/Monitor(\d+)/);
		var idx = match ? parseInt(match[1]) : 0;
		var wp = findWp(m.win_path);
		if (wp) {
			console.log("[wepapered-ui] updateUIState FOUND wp for " + key + ": " + wp.title);
		} else {
			console.log("[wepapered-ui] updateUIState MISSING wp for " + key + " (path=" + m.win_path + ")");
			wp = {
				file: m.win_path,
				type: m.type,
				workshopid: m.workshop_id,
				tags: "",
				title: key
			};
		}

		var wpClone = Object.assign({}, wp);
		wpClone.properties = {};
		wpClone.properties[key] = m.props || {};

		selectedWallpapers[idx] = wpClone;
		if (m.device_path) selectedWallpapers[m.device_path] = wpClone;
		selectedWallpapers[key] = wpClone;
		loc++;
	}
	
	console.log("[wepapered-ui] updateUIState applying config:", monitorsArray, selectedWallpapers);

	val.applyMonitorConfigurationAndWallpaperConfig(
		monitorsArray,
		{ wallpaperconfig: { selectedwallpapers: selectedWallpapers, layout: (state.layout || 0) }, browser: { advertiseworkshop: false, advertiseexplore: false, advertisesendtomobile: false, defaultfilterconfig: { Anime: false } } },
		{},
		false
	);	
	if (val.wallpaperConfig) {
		val.wallpaperConfig.selectedwallpapers = selectedWallpapers;
	}
	if (val.monitors) {
		for (var i = 0; i < val.monitors.length; i++) {
			var loc = val.monitors[i].location;
			if (selectedWallpapers[loc]) {
				val.monitors[i].wallpaper = selectedWallpapers[loc];
			}
		}
	}
	if (val.selectedMonitor && selectedWallpapers[val.selectedMonitor.location]) {
		val.currentSelection = selectedWallpapers[val.selectedMonitor.location];
	}
	
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

// Live workshop download feedback: the daemon relays Steam's progress as
// {type:'wsprogress', workshopid, status, percent, label}. WE's grid shows a
// download ring when a wallpaper object's status is "downloading" and reads its
// downloadpercent/downloadlabel, so we mutate the matching object in place (the
// native client does the same) and trigger a digest.
function findWpByWorkshopId(id) {
	var c = window.browseWallpapersCtrl;
	if (!c) return null;
	var lists = [c.sortedWallpapers, c.queryWallpapers, c.wallpapers];
	for (var li = 0; li < lists.length; li++) {
		var L = lists[li];
		if (!L) continue;
		for (var i = 0; i < L.length; i++) {
			if (L[i] && (String(L[i].workshopid) === String(id) || String(L[i].file) === String(id))) return L[i];
		}
	}
	return null;
}
window.__wsLastStatus = window.__wsLastStatus || {};
function applyWorkshopProgress(msg) {
	var wp = findWpByWorkshopId(msg.workshopid);
	if (!wp) return;
	var c = window.browseWallpapersCtrl;
	var newStatus = msg.status === 'installed' ? 'installed'
		: msg.status === 'downloadable' ? 'downloadable' : 'downloading';
	if (newStatus === 'installed') {
		wp.status = 'installed';
		wp.downloadpercent = 100;
		wp.downloadlabel = '';
	} else if (newStatus === 'downloadable') {
		wp.status = 'downloadable';
		wp.downloadpercent = 0;
		wp.downloadlabel = '';
	} else {
		wp.status = 'downloading';
		if (typeof msg.percent === 'number') wp.downloadpercent = msg.percent;
		wp.downloadlabel = msg.label || '';
	}
	// The virtualised grid only rebuilds a tile (and creates/destroys its
	// download ring) when its sortedWallpapers array reference changes — WE's own
	// refresh trick. Reassign a sliced copy on status transitions; intermediate
	// percent updates flow into the existing ring via its rpPercent watch on a
	// plain digest.
	var changed = window.__wsLastStatus[msg.workshopid] !== newStatus;
	window.__wsLastStatus[msg.workshopid] = newStatus;
	try {
		if (changed && Array.isArray(c.sortedWallpapers)) {
			c.sortedWallpapers = c.sortedWallpapers.slice();
		}
		if (c.$$phase || (c.$root && c.$root.$$phase)) { c.$evalAsync(function(){}); }
		else { c.$apply(); }
	} catch (e) {}
}

// showWepToast shows a transient error banner. Self-contained (inline styles, no
// dependency on WE's Angular/dialog internals) so it works in any UI build.
function showWepToast(text) {
	try {
		var t = document.createElement('div');
		t.textContent = text;
		t.style.cssText = 'position:fixed;top:16px;left:50%;transform:translateX(-50%);' +
			'z-index:2147483647;background:#c0392b;color:#fff;padding:12px 18px;' +
			'border-radius:8px;font-size:14px;font-family:sans-serif;' +
			'box-shadow:0 4px 16px rgba(0,0,0,.4);max-width:80vw;text-align:center;' +
			'opacity:0;transition:opacity .2s';
		document.body.appendChild(t);
		requestAnimationFrame(function(){ t.style.opacity = '1'; });
		setTimeout(function(){
			t.style.opacity = '0';
			setTimeout(function(){ if (t.parentNode) t.parentNode.removeChild(t); }, 300);
		}, 5000);
	} catch (e) {}
}

window.__bridgeOnMessage = function(e) {
	var msg = JSON.parse(e.data);
	if (msg.type === 'state') {
		window.daemonState = msg.state;
		setTimeout(function() { updateUIState(); }, 100);
	} else if (msg.type === 'displays') {
		window.daemonDisplays = msg.displays || [];
		setTimeout(function() { updateUIState(); }, 100);
	} else if (msg.type === 'wsprogress') {
		applyWorkshopProgress(msg);
	} else if (msg.type === 'wserror') {
		showWepToast(msg.message || 'Steam UGC unavailable.');
	} else if (msg.type === 'library') {
		if (window.browseWallpapersCtrl) {
			if (window.browseWallpapersCtrl.setListSource) {
				try { window.browseWallpapersCtrl.setListSource('installed'); } catch(e){}
			}
			window.browseWallpapersCtrl.updateWallpapers(msg.wallpapers, []);
			window.browseWallpapersCtrl.sortWallpapers();
			window.browseWallpapersCtrl.$apply();
			setTimeout(function() { updateUIState(); }, 100);
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
	openLink: function(url) {},
	close: function() { 
		if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.host) window.webkit.messageHandlers.host.postMessage("close"); 
		else window.close(); 
	},
	minimize: function() {
		if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.host) window.webkit.messageHandlers.host.postMessage("minimize");
	},
	maximize: function() {
		if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.host) window.webkit.messageHandlers.host.postMessage("maximize");
	}
};

window.addEventListener('mousedown', function(e) {
	if (e.button !== 0) return;
	var t = e.target;
	while (t && t !== document.body) {
		if (t.tagName === 'BUTTON' || (t.classList && t.classList.contains('titlebarBtn'))) return;
		if (t.classList && t.classList.contains('titlebar')) {
			if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.host) {
				window.webkit.messageHandlers.host.postMessage("drag");
				e.preventDefault();
			}
			return;
		}
		t = t.parentNode;
	}
});

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

// The native WE client normally broadcasts 'onCppInit' on window.rootScope once
// at startup. That handler is what populates the browse controller's filter tag
// lists (D.filterAllTags / filterCategoryTags, from static getAvailableTags()).
// Hosted, nothing fires it, so the Workshop tab crashes in xa() on
// filterAllTags.length. Re-assert it whenever a (browse) controller becomes
// active: poll for rootScope, broadcast once, then re-broadcast ~150ms after
// every route change (mirroring WE's own i(c,100) post-route init delay so the
// controller has registered its $on('onCppInit') before we fire).
(function () {
	// Wallpaper Engine's genre, utility and resolution filter lists are normally
	// pushed by the native client via utils.setAvailableTags(); the type, category,
	// source and rating lists are static in the app. Hosted, setAvailableTags is
	// never called, so the genre ("Tags") and resolution filters come up empty.
	// Seed the service (exposed as window.utilsService) with WE's tag taxonomy —
	// the exact tag strings Steam uses, so the filters also drive the query.
	var WEP_GENRES = ['Abstract', 'Animal', 'Anime', 'Cartoon', 'CGI', 'Cyberpunk',
		'Fantasy', 'Game', 'Girls', 'Guys', 'Landscape', 'Medieval', 'Memes', 'MMD',
		'Music', 'Nature', 'Pixel art', 'Relaxing', 'Retro', 'Sci-Fi', 'Space',
		'Sports', 'Technology', 'Vehicle', 'Unspecified'];
	var WEP_UTILITY = ['Audio responsive', 'Customizable', 'Puppet Warp',
		'Media Integration', 'Video Texture', 'No Animation', 'HDR', 'User Shortcut'];
	// Resolution tags are plain strings; the onCppInit handler maps them itself
	// (label: e.replace(/Standard Definition/g,"SD"), value: e), so passing
	// objects would crash on e.replace.
	var WEP_RESOLUTIONS = ['Standard Definition', '1280 x 720', '1366 x 768',
		'1600 x 900', '1920 x 1080', '2560 x 1440', '3840 x 2160',
		'Ultrawide Standard Definition', 'Ultrawide 2560 x 1080', 'Ultrawide 3440 x 1440',
		'Ultrawide 3840 x 1080', 'Portrait Standard Definition', 'Portrait 720 x 1280',
		'Portrait 1080 x 1920', 'Portrait 1440 x 2560', 'Dual Standard Definition',
		'Dual 7680 x 2160', 'Triple Standard Definition', 'Triple 7680 x 1440',
		'Television', 'Other resolution', 'Dynamic resolution'];

	function seedTags() {
		var u = window.utilsService;
		if (u && typeof u.setAvailableTags === 'function') {
			try { u.setAvailableTags(WEP_GENRES, WEP_UTILITY, WEP_RESOLUTIONS); } catch (e) {}
		}
	}

	function fireCppInit() {
		if (!window.rootScope) return false;
		seedTags();
		try { window.rootScope.$broadcast('onCppInit'); } catch (e) {}
		try {
			var c = window.browseWallpapersCtrl;
			// Drop any bogus genre entry and expand the content-rating section by
			// default so the Mature/Questionable ("-18") toggles are visible.
			if (c && Array.isArray(c.filterAllTags)) {
				c.filterAllTags = c.filterAllTags.filter(function (t) { return t && t.value; });
			}
			if (c) {
				c.filterHideRating = true;
				// The content-rating filter group is gated behind
				// canUseAgeRatingTags(), which only returns true for Valve's
				// internal "steamint" build. Force it on so the Mature/
				// Questionable ("-18") section renders for everyone.
				c.canUseAgeRatingTags = function () { return true; };
			}
			if (window.utilsService) {
				window.utilsService.canUseAgeRatingTags = function () { return true; };
			}
		} catch (e) {}
		return true;
	}
	var iv = setInterval(function () {
		if (!fireCppInit()) return;
		clearInterval(iv);
		try {
			window.rootScope.$on('$routeChangeSuccess', function () {
				setTimeout(fireCppInit, 150);
			});
		} catch (e) {}
	}, 100);
})();

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
						// WE's callDeferred(obj, method, …) registers its reply
						// handler as window[method + "Callback"] and resolves a
						// promise from it. Send that exact callback name so the
						// daemon's echoed reply lands on the handler callDeferred
						// is waiting on (a random name would resolve nothing and
						// the caller's promise would hang — e.g. the Workshop
						// query spinner never clearing).
						sendToBridge({
							object: name,
							method: String(prop),
							args: args,
							callback: String(prop) + 'Callback'
						});
						// OK / Cancel both close the window; the daemon does the
						// save (accept) or rollback (cancel) on its side.
						if (name === 'browseWallpaperObject' && (prop === 'acceptAndClose' || prop === 'cancelAndClose')) {
							if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.host) window.webkit.messageHandlers.host.postMessage('close');
							else if (window.close) window.close();
						}
					};
				}
			}),
			writable: true,
			configurable: true
		});
	} catch(e){}
});

</script>
		<style>
			help-overlay, .ui-tutorial, [class*="tutorial"], .welcome-screen { display: none !important; }
			.popover, .tooltip { display: none !important; opacity: 0 !important; }
			[translate^="ui_browse_advertise"] { display: none !important; }
		</style>`
		
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
