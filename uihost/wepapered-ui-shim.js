// wepapered hosted-UI shim.
//
// Injected (before any page script) into Wallpaper Engine's own browse UI
// (ui/dist/index.html) when we host it ourselves in a CEF window — no WE/Proton.
// It stands in for WE's native bridge by routing everything over the daemon
// WebSocket (ws://127.0.0.1:9001/we):
//
//   • window.global         — boot namespace the UI needs (_wpxLocale, jsInit)
//   • window.<bridgeObject> — every method call is forwarded to the daemon;
//                             the daemon replies {callback, args} which we
//                             dispatch to window[callback] (the contract
//                             callDeferred() expects).
//   • library / locale      — pushed by the daemon on connect; we inject the
//                             library into browseWallpapersCtrl.updateWallpapers.
//
// The daemon does the real browse-only work (enumerate library, select, etc.).

(function () {
  'use strict';

  var WS_URL = 'ws://127.0.0.1:9001/we';
  var ws = null;
  var sendQueue = [];
  var library = null;       // [] once received from the daemon
  var pushTries = 0;

  window.__wepLocale = window.__wepLocale || {};

  function wlog(m) { try { console.log('[wepapered-shim] ' + m); } catch (e) {} }

  // ── Boot namespace the UI reads before bootstrap ────────────────────────────
  window.global = {
    _wpxLocale: function (key) {
      var d = window.__wepLocale;
      return (d && Object.prototype.hasOwnProperty.call(d, key)) ? d[key] : key;
    },
    jsInit: function () {
      // The UI calls this on route change to request a provider connection.
      // Our "provider" is the daemon; the library push may already be queued.
      tryPushLibrary();
    }
  };

  // ── WebSocket transport ─────────────────────────────────────────────────────
  function connect() {
    try {
      ws = new WebSocket(WS_URL);
    } catch (e) { setTimeout(connect, 2000); return; }

    ws.onopen = function () {
      wlog('connected');
      var q = sendQueue; sendQueue = [];
      q.forEach(function (m) { try { ws.send(m); } catch (e) {} });
    };
    ws.onclose = function () { ws = null; setTimeout(connect, 2000); };
    ws.onerror = function () { };
    ws.onmessage = function (ev) {
      var msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }

      if (msg.type === 'library') { library = msg.wallpapers || []; wlog('library: ' + library.length); tryPushLibrary(); return; }
      if (msg.type === 'locale') { window.__wepLocale = msg.table || {}; wlog('locale: ' + Object.keys(window.__wepLocale).length); return; }
      if (msg.type === 'state') { return; }
      // Bridge reply: window[method + "Callback"](...args)
      if (msg.callback && typeof window[msg.callback] === 'function') {
        try { window[msg.callback].apply(null, msg.args || []); } catch (e) {}
      }
    };
  }

  function send(obj) {
    var s = JSON.stringify(obj);
    if (ws && ws.readyState === 1) { try { ws.send(s); } catch (e) {} }
    else if (sendQueue.length < 500) { sendQueue.push(s); }
  }

  // ── Native bridge objects → forward to daemon ───────────────────────────────
  var BRIDGE_OBJECTS = [
    'browseWallpaperObject', 'settingsObject', 'ui', 'welcomeObject',
    'editorObject', 'installObject', 'workshopObject'
  ];
  BRIDGE_OBJECTS.forEach(function (name) {
    var impl = new Proxy({}, {
      get: function (target, prop) {
        if (typeof prop === 'symbol') return undefined;
        if (prop === '__wepShim') return true;
        return function () {
          send({ object: name, method: String(prop), args: Array.prototype.slice.call(arguments) });
        };
      }
    });
    try {
      Object.defineProperty(window, name, { value: impl, writable: true, configurable: true });
    } catch (e) { window[name] = impl; }
  });

  // ── Library push into the browse controller ────────────────────────────────
  function tryPushLibrary() {
    if (!library) return;
    var c = window.browseWallpapersCtrl;
    if (!c || typeof c.updateWallpapers !== 'function') {
      if (++pushTries < 80) setTimeout(tryPushLibrary, 250);
      return;
    }
    try {
      if (typeof c.setListSource === 'function') c.setListSource('installed');
      c.updateWallpapers(library, []);
      if (typeof c.sortWallpapers === 'function') c.sortWallpapers();
      c.$apply();
      wlog('pushed ' + library.length + ' wallpapers');
    } catch (e) {
      if (++pushTries < 80) setTimeout(tryPushLibrary, 250);
    }
  }

  connect();
})();
