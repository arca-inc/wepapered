// Phase-0 probe driver: load the real WE UI (ui/dist/index.html) in headless
// Chromium via Playwright, inject shim-logger.js in place of the native bridge,
// and dump every [PROBE] line so we learn exactly what the native side must do.
//
// Usage: node tools/uiprobe/probe.mjs [seconds]

import { chromium } from 'playwright-core';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import os from 'node:os';

const __dirname = dirname(fileURLToPath(import.meta.url));
const WE = join(os.homedir(), '.local/share/Steam/steamapps/common/wallpaper_engine');
const UI_DIR = join(WE, 'ui/dist');
const INDEX = join(UI_DIR, 'index.html');
const SHIM = readFileSync(join(__dirname, 'shim-logger.js'), 'utf8');
const SECONDS = Number(process.argv[2] || 12);

const CHROME = join(os.homedir(), '.cache/ms-playwright/chromium-1223/chrome-linux64/chrome');

const browser = await chromium.launch({
  executablePath: CHROME,
  headless: true,
  args: ['--no-sandbox', '--disable-gpu', '--allow-file-access-from-files'],
});

const ctx = await browser.newContext();
const page = await ctx.newPage();

// Inject the shim BEFORE any page script runs, so the bridge objects exist
// before Angular bootstraps.
await page.addInitScript(SHIM);

const probeLines = [];
page.on('console', (msg) => {
  const t = msg.text();
  if (t.startsWith('[PROBE] ')) probeLines.push(t.slice(8));
  else if (t.toLowerCase().includes('error')) probeLines.push(JSON.stringify({ kind: 'console', text: t }));
});
page.on('pageerror', (err) => probeLines.push(JSON.stringify({ kind: 'pageerror', detail: String(err) })));

// The UI reads query params (skinStyle, skinKey, …); the route is a hash.
// Default route is /empty; the native normally navigates to /browsewallpapers.
const route = process.argv[3] || '/browsewallpapers';
const url = 'file://' + INDEX + '?skinStyle=styles/main.css&skinKey=default#' + route;
try {
  await page.goto(url, { waitUntil: 'load', timeout: 15000 });
} catch (e) {
  probeLines.push(JSON.stringify({ kind: 'goto-error', detail: String(e) }));
}

await page.waitForTimeout(SECONDS * 1000);

// Enumerate the PUSH API: methods the native calls on the registered controllers
// to inject the library / drive the UI. This is the half of the bridge that
// callDeferred() does NOT cover.
const pushApi = await page.evaluate(() => {
  function methodsOf(obj) {
    if (!obj) return null;
    const out = new Set();
    for (let o = obj; o && o !== Object.prototype; o = Object.getPrototypeOf(o)) {
      for (const k of Object.getOwnPropertyNames(o)) {
        try { if (typeof obj[k] === 'function' && k !== 'constructor') out.add(k); } catch {}
      }
    }
    return [...out].sort();
  }
  function dataKeys(obj) {
    if (!obj) return null;
    const out = [];
    for (const k of Object.keys(obj)) {
      let t; try { t = typeof obj[k]; } catch { t = '??'; }
      if (t !== 'function') out.push(k + ':' + t);
    }
    return out.sort();
  }
  const ctrls = ['browseWallpapersCtrl', 'settingsCtrl', 'welcomeCtrl', 'editorCtrl'];
  const r = {};
  for (const c of ctrls) r[c] = { methods: methodsOf(window[c]), data: dataKeys(window[c]) };
  // Receiver-style globals the native calls to push data (window.X = fn).
  r._globalFns = Object.keys(window).filter((k) => {
    try { return typeof window[k] === 'function' && /^(on|receive|ctrlReceive|callback|set|add|update|refresh|insert)/i.test(k); }
    catch { return false; }
  }).sort();
  return r;
});

await browser.close();

// ── Report ────────────────────────────────────────────────────────────────
const events = probeLines.map((l) => { try { return JSON.parse(l); } catch { return { kind: 'raw', text: l }; } });

const calls = events.filter((e) => e.kind === 'call');
const globals = events.filter((e) => e.kind === 'global');
const errors = events.filter((e) => e.kind === 'error' || e.kind === 'pageerror' || e.kind === 'goto-error');

const byMethod = {};
for (const c of calls) {
  const key = c.detail.obj + '.' + c.detail.method;
  (byMethod[key] ||= []).push(c.detail.args);
}

console.log('\n================ BOOT CALL SEQUENCE (first 60) ================');
for (const c of calls.slice(0, 60)) {
  console.log(`  ${String(c.n).padStart(3)}  ${c.detail.obj}.${c.detail.method}(${JSON.stringify(c.detail.args).slice(1, -1)})`);
}

console.log('\n================ NATIVE METHODS CALLED (count) ================');
for (const [k, v] of Object.entries(byMethod).sort((a, b) => b[1].length - a[1].length)) {
  console.log(`  ${String(v.length).padStart(3)}x  ${k}   e.g. args=${JSON.stringify(v[0])}`);
}

console.log('\n================ GLOBALS REGISTERED BY THE UI (push channels) ================');
for (const g of globals) console.log(`  window.${g.detail.name}  (${g.detail.type})`);

console.log('\n================ ERRORS ================');
for (const e of errors.slice(0, 30)) console.log('  ' + JSON.stringify(e.detail || e.text));

console.log('\n================ PUSH API (native → UI controllers) ================');
for (const [ctrl, info] of Object.entries(pushApi)) {
  if (ctrl === '_globalFns') continue;
  if (!info.methods) { console.log(`  ${ctrl}: <not registered>`); continue; }
  console.log(`  ${ctrl}.methods: ${info.methods.join(', ')}`);
  console.log(`  ${ctrl}.data:    ${(info.data || []).join(', ')}`);
}
console.log('\n  receiver-style global fns:', (pushApi._globalFns || []).join(', '));

console.log(`\nTotal: ${calls.length} native calls, ${globals.length} globals, ${errors.length} errors.`);
