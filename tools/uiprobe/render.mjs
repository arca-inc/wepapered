// Phase-0 ACTIVE probe: push the real local library into the WE UI via
// browseWallpapersCtrl.updateWallpapers() and screenshot the result, to prove
// the grid renders and to lock down the wallpaper-object shape.
//
// Usage: node render.mjs [seconds]

import { chromium } from 'playwright-core';
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import os from 'node:os';

const __dirname = dirname(fileURLToPath(import.meta.url));
const WE = join(os.homedir(), '.local/share/Steam/steamapps/common/wallpaper_engine');
const STEAMAPPS = join(WE, '..', '..');
const UI_DIR = join(WE, 'ui/dist');
const INDEX = join(UI_DIR, 'index.html');
const SHIM = readFileSync(join(__dirname, 'shim-logger.js'), 'utf8');
const SECONDS = Number(process.argv[2] || 8);
const CHROME = join(os.homedir(), '.cache/ms-playwright/chromium-1223/chrome-linux64/chrome');

// ── Build the wallpaper library the way the daemon eventually will ──────────
function buildLibrary() {
  const root = join(STEAMAPPS, 'workshop/content/431960');
  const out = [];
  let ids = [];
  try { ids = readdirSync(root); } catch { return out; }
  for (const id of ids) {
    const dir = join(root, id);
    const pj = join(dir, 'project.json');
    if (!existsSync(pj)) continue;
    let meta;
    try { meta = JSON.parse(readFileSync(pj, 'utf8')); } catch { continue; }
    const previewFile = meta.preview || 'preview.jpg';
    const previewUrl = 'file://' + join(dir, previewFile);
    out.push({
      file: dir,                       // identity key; daemon resolves this on selectWallpaper
      title: meta.title || id,
      type: meta.type || '',
      preview: previewUrl,
      previewsmall: previewUrl,
      workshopid: String(meta.workshopid || id),
      itemid: String(meta.workshopid || id),
      contentrating: meta.contentrating || 'Everyone',
      tags: meta.tags || [],
      status: 'installed',
      local: false,
      approved: true,
    });
  }
  return out;
}

// If LIB_JSON points at a file (e.g. the daemon's `--dump-library` output),
// render THAT — proving the Go-produced data drives the real UI end-to-end.
const library = process.env.LIB_JSON
  ? JSON.parse(readFileSync(process.env.LIB_JSON, 'utf8'))
  : buildLibrary();
console.log(`Built library: ${library.length} wallpapers. Sample:`, JSON.stringify(library[0], null, 0).slice(0, 200));

const browser = await chromium.launch({
  executablePath: CHROME, headless: true,
  args: ['--no-sandbox', '--disable-gpu', '--allow-file-access-from-files'],
});
const ctx = await browser.newContext({ viewport: { width: 1500, height: 950 } });
const page = await ctx.newPage();

// Seed real translations: merge WE's core_ + ui_ locale JSON (en-us) so
// _wpxLocale resolves keys to strings. This is exactly what the daemon will serve.
function loadLocale(lang) {
  const dict = {};
  for (const prefix of ['core', 'ui']) {
    const f = join(WE, 'locale', `${prefix}_${lang}.json`);
    try { Object.assign(dict, JSON.parse(readFileSync(f, 'utf8'))); } catch {}
  }
  return dict;
}
const locale = loadLocale('en-us');
console.log(`Loaded locale: ${Object.keys(locale).length} keys`);
await page.addInitScript({ content: 'window.__wepLocale=' + JSON.stringify(locale) });
await page.addInitScript(SHIM);

const logs = [];
page.on('console', (m) => { const t = m.text(); if (t.startsWith('[PROBE] ') || /error/i.test(t)) logs.push(t.slice(0, 200)); });
page.on('pageerror', (e) => logs.push('PAGEERROR ' + String(e).slice(0, 200)));

const url = 'file://' + INDEX + '?skinStyle=styles/main.css&skinKey=default#/browsewallpapers';
await page.goto(url, { waitUntil: 'load', timeout: 15000 });
await page.waitForTimeout(2500); // let Angular settle + controller register

// ── Push the library ────────────────────────────────────────────────────────
const result = await page.evaluate((lib) => {
  const c = window.browseWallpapersCtrl;
  if (!c) return { ok: false, why: 'browseWallpapersCtrl not registered' };
  const steps = [];
  try {
    if (typeof c.setListSource === 'function') { c.setListSource('installed'); steps.push('setListSource(installed)'); }
    if (typeof c.updateWallpapers === 'function') { c.updateWallpapers(lib, []); steps.push('updateWallpapers(' + lib.length + ')'); }
    if (typeof c.sortWallpapers === 'function') { c.sortWallpapers(); steps.push('sortWallpapers'); }
    c.$apply();
  } catch (e) {
    return { ok: false, why: String(e), steps, wallpapers: (c.wallpapers || []).length };
  }
  return {
    ok: true, steps,
    source: c.source,
    sourceIsInstalled: c.sourceIsInstalled,
    wallpapers: (c.wallpapers || []).length,
    sortedWallpapers: (c.sortedWallpapers || []).length,
    unfilteredCount: c.unfilteredCount,
    domTiles: document.querySelectorAll('[style*="background-image"], .wallpaper, .wpItem, .browseItem').length,
  };
}, library);

await page.waitForTimeout(SECONDS * 1000);
await page.screenshot({ path: join(__dirname, 'render.png'), fullPage: false });
await browser.close();

console.log('\n=== PUSH RESULT ===');
console.log(JSON.stringify(result, null, 2));
console.log('\n=== LOGS (tail) ===');
for (const l of logs.slice(-25)) console.log('  ' + l);
console.log('\nScreenshot: tools/uiprobe/render.png');
