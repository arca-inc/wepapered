// End-to-end test of the PRODUCTION hosted-UI bridge:
//   real daemon (ws://127.0.0.1:9001)  ⇄  uihost/wepapered-ui-shim.js  ⇄  WE UI
// Playwright stands in for the CEF window. The legacy wepapered-inject.js is
// neutralised to simulate a pristine (fresh-install) ui/dist/index.html.
//
// Prereq: run the daemon first (it must be serving :9001).
// Usage: node hosted.mjs [seconds]

import { chromium } from 'playwright-core';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import os from 'node:os';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO = join(__dirname, '..', '..');
const WE = join(os.homedir(), '.local/share/Steam/steamapps/common/wallpaper_engine');
const INDEX = join(WE, 'ui/dist/index.html');
const SHIM = readFileSync(join(REPO, 'uihost/wepapered-ui-shim.js'), 'utf8');
const SECONDS = Number(process.argv[2] || 6);
const CHROME = join(os.homedir(), '.cache/ms-playwright/chromium-1223/chrome-linux64/chrome');

const browser = await chromium.launch({
  executablePath: CHROME, headless: true,
  args: ['--no-sandbox', '--disable-gpu', '--allow-file-access-from-files'],
});
const ctx = await browser.newContext({ viewport: { width: 1500, height: 950 } });
const page = await ctx.newPage();

// Simulate a fresh install: pretend the legacy inject patch isn't there.
await ctx.route('**/wepapered-inject.js', (r) => r.fulfill({ contentType: 'application/javascript', body: '' }));

// Inject ONLY the production shim, before page scripts (what the CEF host does).
await page.addInitScript(SHIM);

const logs = [];
page.on('console', (m) => { const t = m.text(); if (/wepapered-shim|error/i.test(t)) logs.push(t.slice(0, 160)); });
page.on('pageerror', (e) => logs.push('PAGEERROR ' + String(e).slice(0, 160)));

await page.goto('file://' + INDEX + '?skinStyle=styles/main.css&skinKey=default#/browsewallpapers', { waitUntil: 'load', timeout: 15000 });
await page.waitForTimeout(SECONDS * 1000);

const result = await page.evaluate(() => {
  const c = window.browseWallpapersCtrl;
  return {
    controllerReady: !!(c && typeof c.updateWallpapers === 'function'),
    wallpapers: c ? (c.wallpapers || []).length : -1,
    domTiles: document.querySelectorAll('[style*="background-image"]').length,
    localeKeys: Object.keys(window.__wepLocale || {}).length,
    bridgeInstalled: !!(window.browseWallpaperObject && window.browseWallpaperObject.__wepShim),
  };
});

await page.screenshot({ path: join(__dirname, 'hosted.png') });
await browser.close();

console.log('=== HOSTED BRIDGE RESULT ===');
console.log(JSON.stringify(result, null, 2));
console.log('\n=== SHIM LOGS ===');
for (const l of logs.slice(-15)) console.log('  ' + l);
console.log('\nScreenshot: tools/uiprobe/hosted.png');
