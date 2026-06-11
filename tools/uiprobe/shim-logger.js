// Phase-0 probe shim: stand in for Wallpaper Engine's native bridge objects.
// Goal: observe exactly what the WE Angular UI calls on the native side at boot,
// and which global receivers it registers — WITHOUT running WE/Proton.
//
// Loaded in place of wepapered-inject.js. Every call is logged to the page
// console with a [PROBE] prefix; the Playwright driver forwards those to stdout.

(function () {
  'use strict';

  var SEQ = 0;
  function log(kind, detail) {
    try {
      console.log('[PROBE] ' + JSON.stringify({ n: ++SEQ, kind: kind, detail: detail }));
    } catch (e) {
      console.log('[PROBE] ' + kind + ' ' + detail);
    }
  }

  function safeArgs(args) {
    return Array.prototype.slice.call(args).map(function (a) {
      var t = typeof a;
      if (a === null) return null;
      if (t === 'function') return '<fn>';
      if (t === 'object') {
        try { return JSON.parse(JSON.stringify(a)); } catch (e) { return '<obj?>'; }
      }
      return a;
    });
  }

  // The native bridge objects WE injects into the V8 context. The UI calls
  // window[obj][method](...) and expects a later window[method+'Callback'](...).
  var BRIDGE_OBJECTS = [
    'browseWallpaperObject', 'settingsObject', 'ui', 'welcomeObject',
    'editorObject', 'installObject', 'workshopObject'
  ];

  function makeBridge(name) {
    return new Proxy({}, {
      get: function (target, prop) {
        if (prop === '__wepProbe') return true;
        if (typeof prop === 'symbol') return undefined;
        // Every property access returns a logging function. WE only ever calls
        // methods on these, so returning a function for any prop is safe.
        return function () {
          log('call', { obj: name, method: String(prop), args: safeArgs(arguments) });
          // No callback is fired: we want to see what the UI does without data,
          // and which methods it retries / depends on for boot.
          return undefined;
        };
      },
      set: function (target, prop, value) { target[prop] = value; return true; }
    });
  }

  // The native injects `window.global`, the core native namespace. The UI's
  // boot/translate layer reads window.global._wpxLocale(key) and calls
  // window.global.jsInit() to start. Seed a minimal proxy so bootstrap proceeds
  // and we can observe the real bridge calls that follow.
  var globalImpl = {
    _wpxLocale: function (key) {
      // Real translations come from WE's locale JSON, seeded as window.__wepLocale.
      var d = window.__wepLocale;
      if (d && Object.prototype.hasOwnProperty.call(d, key)) return d[key];
      return key;
    },
    jsInit: function () { log('global.jsInit', { args: safeArgs(arguments) }); }
  };
  var globalProxy = new Proxy(globalImpl, {
    get: function (target, prop) {
      if (prop in target) return target[prop];
      if (typeof prop === 'symbol') return undefined;
      log('global.GET', { prop: String(prop) });
      return function () {
        log('global.call', { method: String(prop), args: safeArgs(arguments) });
        return undefined;
      };
    }
  });
  try {
    Object.defineProperty(window, 'global', { value: globalProxy, writable: true, configurable: true });
  } catch (e) { window.global = globalProxy; }

  BRIDGE_OBJECTS.forEach(function (name) {
    try {
      Object.defineProperty(window, name, {
        value: makeBridge(name), writable: true, configurable: true
      });
    } catch (e) {
      window[name] = makeBridge(name);
    }
  });

  // Log every global the app registers (window.X = ...) so we discover the
  // PUSH channels (browseWallpapersCtrl, rootScope, *Callback receivers, …).
  var WATCH_PREFIXES = null; // null = log all new globals
  var known = {};
  Object.keys(window).forEach(function (k) { known[k] = true; });
  setInterval(function () {
    Object.keys(window).forEach(function (k) {
      if (known[k]) return;
      known[k] = true;
      var v;
      try { v = typeof window[k]; } catch (e) { v = '??'; }
      log('global', { name: k, type: v });
    });
  }, 200);

  // Capture unhandled errors — the UI may throw when a native call returns nothing.
  window.addEventListener('error', function (e) {
    log('error', { msg: String(e.message), src: (e.filename || '') + ':' + e.lineno });
  });

  log('boot', { href: location.href });
})();
