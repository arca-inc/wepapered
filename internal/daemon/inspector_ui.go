package daemon

import "net/http"

// serveInspector serves the standalone scene debug inspector — a self-contained
// page (no WE/Angular dependency) that talks to the daemon's /inspect + /debug
// APIs: it lists introspectable monitors, fetches the live scene graph, renders an
// object tree + property panel + console, and drives the live debug controls
// (isolate/hide via objectFilter/skipObjects) and per-object property edits.
func (s *WSServer) serveInspector(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(inspectorHTML))
}

const inspectorHTML = `<!doctype html>
<html lang="fr">
<head>
<meta charset="utf-8">
<title>Inspecteur de scène — WePapered</title>
<style>
  :root {
    --bg: #1e1f24; --panel: #26282f; --panel2: #2d2f37; --line: #383b44;
    --fg: #e6e6ea; --muted: #9aa0ac; --accent: #5aa9ff; --accent2: #ffb454;
    --hidden: #6b6f78; --mono: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; margin: 0; }
  body { background: var(--bg); color: var(--fg); font: 13px/1.5 system-ui, sans-serif; display: flex; flex-direction: column; }
  header {
    display: flex; align-items: center; gap: 12px; padding: 8px 12px;
    background: var(--panel); border-bottom: 1px solid var(--line); flex: 0 0 auto;
  }
  header h1 { font-size: 14px; font-weight: 600; margin: 0; }
  header .sp { flex: 1; }
  select, button {
    background: var(--panel2); color: var(--fg); border: 1px solid var(--line);
    border-radius: 6px; padding: 5px 10px; font: inherit; cursor: pointer;
  }
  button:hover, select:hover { border-color: var(--accent); }
  button.warn { border-color: #a05; }
  label.chk { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); cursor: pointer; }
  main { flex: 1; display: flex; min-height: 0; }
  #tree { width: 44%; overflow: auto; border-right: 1px solid var(--line); padding: 6px 0; }
  #side { flex: 1; display: flex; flex-direction: column; min-width: 0; }
  #props { flex: 1; overflow: auto; padding: 12px; }
  #console {
    flex: 0 0 26%; overflow: auto; border-top: 1px solid var(--line);
    background: #16171b; padding: 8px 12px; font-family: var(--mono); font-size: 12px; white-space: pre-wrap;
  }
  .row {
    display: flex; align-items: center; gap: 6px; padding: 3px 8px; cursor: pointer;
    white-space: nowrap; user-select: none;
  }
  .row:hover { background: var(--panel2); }
  .row.sel { background: #34537a; }
  .row.hidden .name { color: var(--hidden); text-decoration: line-through; }
  .row.iso { box-shadow: inset 2px 0 0 var(--accent2); }
  .tw { width: 14px; text-align: center; color: var(--muted); flex: 0 0 auto; }
  .ico {
    flex: 0 0 auto; width: 22px; text-align: center; border-radius: 4px; padding: 1px 0;
    border: 1px solid transparent; font-size: 12px; color: var(--muted);
  }
  .ico:hover { border-color: var(--accent); color: var(--fg); }
  .ico.on { color: var(--accent2); border-color: var(--accent2); }
  .badge {
    font-size: 10px; padding: 1px 6px; border-radius: 4px; background: var(--line);
    color: var(--muted); flex: 0 0 auto; text-transform: uppercase; letter-spacing: .04em;
  }
  .badge.compose { background: #5a3a7a; color: #e6d0ff; }
  .badge.image { background: #2f5a45; color: #c8f0d8; }
  .badge.text { background: #5a4a2f; color: #ffe6c0; }
  .badge.particle { background: #2f475a; color: #c0e6ff; }
  .badge.sound { background: #5a2f3a; color: #ffc8d4; }
  .name { overflow: hidden; text-overflow: ellipsis; flex: 1; }
  .id { color: var(--muted); font-family: var(--mono); font-size: 11px; }
  #props h2 { margin: 0 0 4px; font-size: 15px; }
  #props .sub { color: var(--muted); margin-bottom: 14px; font-family: var(--mono); font-size: 12px; }
  table.kv { border-collapse: collapse; width: 100%; }
  table.kv td { padding: 5px 8px; border-bottom: 1px solid var(--line); vertical-align: middle; }
  table.kv td.k { color: var(--muted); width: 120px; }
  table.kv td.v { font-family: var(--mono); }
  .empty { color: var(--muted); padding: 24px; text-align: center; }
  .tag { display: inline-block; background: var(--panel2); border: 1px solid var(--line); border-radius: 4px; padding: 1px 6px; margin: 2px 4px 2px 0; font-family: var(--mono); font-size: 11px; }
  .clog-err { color: #ff8080; }
  .clog-ok { color: #80d890; }
  input[type=number] {
    width: 74px; background: #16171b; color: var(--fg); border: 1px solid var(--line);
    border-radius: 4px; padding: 3px 6px; font-family: var(--mono); font-size: 12px;
  }
  input[type=number]:focus { border-color: var(--accent); outline: none; }
  .edithint { color: var(--accent2); font-size: 11px; margin: 10px 0 4px; }
</style>
</head>
<body>
<header>
  <h1>🔍 Inspecteur de scène</h1>
  <select id="output" title="Moniteur"></select>
  <button id="refresh">↻ Rafraîchir</button>
  <label class="chk"><input type="checkbox" id="auto"> auto (1s)</label>
  <button id="reset" class="warn" title="Réafficher tout + annuler isolation">⟲ Reset debug</button>
  <div class="sp"></div>
  <span id="stat" class="id"></span>
</header>
<main>
  <div id="tree"></div>
  <div id="side">
    <div id="props"><div class="empty">Sélectionne un objet dans l'arbre.</div></div>
    <div id="console"></div>
  </div>
</main>
<script>
var $ = function(s){ return document.querySelector(s); };
// hidden/isolated are client-side mirrors of the LWE debug state (skipObjects /
// objectFilter), which the inspect JSON doesn't report back.
var state = { data: null, sel: null, autoTimer: null, hidden: {}, isolated: null };

function log(msg, cls) {
  var c = $('#console');
  var span = document.createElement('span');
  if (cls) span.className = cls;
  span.textContent = '[' + new Date().toLocaleTimeString() + '] ' + msg + '\n';
  c.appendChild(span);
  c.scrollTop = c.scrollHeight;
}

function api(path) {
  return fetch(path, { cache: 'no-store' }).then(function(r){
    if (!r.ok) return r.text().then(function(t){ throw new Error(r.status + ' ' + t); });
    return r.json();
  });
}

// dbg POSTs a debug command for the current output and logs the ack.
function dbg(cmd, extra) {
  var out = $('#output').value;
  if (!out) return Promise.resolve();
  var body = Object.assign({ output: out, cmd: cmd }, extra || {});
  return fetch('/debug', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
  }).then(function(r){ return r.json(); }).then(function(ack){
    if (ack && ack.ok === false) log(cmd + ' → ' + (ack.error || 'échec'), 'clog-err');
    else log(cmd + ' ' + JSON.stringify(extra || {}), 'clog-ok');
    return ack;
  }).catch(function(e){ log(cmd + ': ' + e.message, 'clog-err'); });
}

function loadOutputs() {
  return api('/inspect').then(function(j){
    var sel = $('#output'), prev = sel.value;
    sel.innerHTML = '';
    (j.outputs || []).forEach(function(name){
      var o = document.createElement('option');
      o.value = name; o.textContent = name; sel.appendChild(o);
    });
    if (prev && (j.outputs || []).indexOf(prev) >= 0) sel.value = prev;
    if (!j.outputs || !j.outputs.length) {
      $('#tree').innerHTML = '<div class="empty">Aucun moniteur de scène actif.<br>(les fonds web/vidéo/image ne sont pas inspectables)</div>';
      log('aucun moniteur inspectable', 'clog-err');
    }
    return j.outputs || [];
  });
}

function fetchScene() {
  var out = $('#output').value;
  if (!out) return;
  $('#stat').textContent = 'chargement ' + out + '…';
  api('/inspect?output=' + encodeURIComponent(out)).then(function(j){
    state.data = j;
    renderTree();
    if (state.sel != null) renderProps(findObj(state.sel));
    var n = (j.objects || []).length;
    $('#stat').textContent = out + ' — ' + n + ' objets — scène ' + (j.scene ? j.scene.width + '×' + j.scene.height : '?');
  }).catch(function(e){
    $('#stat').textContent = 'erreur';
    log('inspect: ' + e.message, 'clog-err');
  });
}

function findObj(id) {
  return (state.data.objects || []).filter(function(o){ return o.id === id; })[0] || null;
}

function renderTree() {
  var objs = state.data.objects || [];
  var byId = {}; objs.forEach(function(o){ byId[o.id] = o; });
  var children = {}, roots = [];
  objs.forEach(function(o){
    var p = o.parent;
    if (p != null && byId[p]) { (children[p] = children[p] || []).push(o); }
    else { roots.push(o); }
  });
  var tree = $('#tree'); tree.innerHTML = '';
  var seen = {};
  function emit(o, depth) {
    if (seen[o.id]) return; seen[o.id] = true;
    tree.appendChild(rowFor(o, depth));
    (children[o.id] || []).forEach(function(c){ emit(c, depth + 1); });
  }
  roots.forEach(function(o){ emit(o, 0); });
  objs.forEach(function(o){ if (!seen[o.id]) emit(o, 0); });
}

function mkIco(txt, title, on, handler) {
  var b = document.createElement('span');
  b.className = 'ico' + (on ? ' on' : ''); b.textContent = txt; b.title = title;
  b.onclick = function(e){ e.stopPropagation(); handler(); };
  return b;
}

function rowFor(o, depth) {
  var isHidden = !!state.hidden[o.id];
  var isIso = state.isolated === o.id;
  var div = document.createElement('div');
  div.className = 'row' + (state.sel === o.id ? ' sel' : '') + (isHidden || o.visible === false ? ' hidden' : '') + (isIso ? ' iso' : '');
  div.style.paddingLeft = (8 + depth * 16) + 'px';
  var tw = document.createElement('span'); tw.className = 'tw'; tw.textContent = '•';
  var badge = document.createElement('span'); badge.className = 'badge ' + (o.type || 'object'); badge.textContent = o.type || 'obj';
  var name = document.createElement('span'); name.className = 'name'; name.textContent = o.name || '(sans nom)';
  var id = document.createElement('span'); id.className = 'id'; id.textContent = '#' + o.id;
  var eye = mkIco(isHidden ? '🚫' : '👁', isHidden ? 'Réafficher' : 'Masquer', isHidden, function(){
    var nowHidden = !state.hidden[o.id];
    if (nowHidden) state.hidden[o.id] = true; else delete state.hidden[o.id];
    dbg(nowHidden ? 'hide' : 'show', { id: o.id });
    renderTree();
  });
  var iso = mkIco('🎯', 'Isoler', isIso, function(){
    if (state.isolated === o.id) { state.isolated = null; dbg('isolate', {}); }
    else { state.isolated = o.id; dbg('isolate', { id: o.id }); }
    renderTree();
  });
  div.appendChild(tw); div.appendChild(badge); div.appendChild(name); div.appendChild(id);
  div.appendChild(eye); div.appendChild(iso);
  div.onclick = function(){ state.sel = o.id; renderTree(); renderProps(o); };
  return div;
}

function fmtVec(v) {
  if (!Array.isArray(v)) return String(v);
  return '[' + v.map(function(n){ return (typeof n === 'number') ? +n.toFixed(2) : n; }).join(', ') + ']';
}
function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"]/g, function(c){
    return { '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;' }[c];
  });
}

// Build a number input that sends a "set" command on change.
function numInput(val, onChange) {
  var i = document.createElement('input');
  i.type = 'number'; i.step = 'any'; i.value = (val == null ? 0 : +val);
  i.onchange = function(){ onChange(parseFloat(i.value) || 0); };
  return i;
}

function renderProps(o) {
  var p = $('#props');
  if (!o) { p.innerHTML = '<div class="empty">Sélectionne un objet.</div>'; return; }
  p.innerHTML = '<h2>' + escapeHtml(o.name || '(sans nom)') + '</h2>' +
    '<div class="sub">' + (o.type || 'object') + ' · #' + o.id + (o.parent != null ? ' · parent #' + o.parent : '') + '</div>';

  var tbl = document.createElement('table'); tbl.className = 'kv';
  function rowKV(k, vNode) {
    var tr = document.createElement('tr');
    var tk = document.createElement('td'); tk.className = 'k'; tk.textContent = k;
    var tv = document.createElement('td'); tv.className = 'v';
    if (typeof vNode === 'string') tv.innerHTML = vNode; else tv.appendChild(vNode);
    tr.appendChild(tk); tr.appendChild(tv); tbl.appendChild(tr);
  }
  function vecEditor(prop, vec, n) {
    var wrap = document.createElement('span');
    var arr = (vec || []).slice(0, n);
    while (arr.length < n) arr.push(0);
    arr.forEach(function(comp, idx){
      var inp = numInput(comp, function(nv){
        arr[idx] = nv; dbg('set', { id: o.id, prop: prop, value: arr });
      });
      inp.style.marginRight = '4px';
      wrap.appendChild(inp);
    });
    return wrap;
  }

  rowKV('id', String(o.id));
  rowKV('type', o.type);

  // ── Édition live (Phase 4.5) ──
  var hint = document.createElement('div'); hint.className = 'edithint';
  hint.textContent = '✎ Édition live — appliquée immédiatement (les valeurs scriptées, ex. horloge, reviennent chaque frame).';
  p.appendChild(hint);
  p.appendChild(tbl);

  if ('visible' in o) {
    var cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = o.visible !== false;
    cb.onchange = function(){ dbg('set', { id: o.id, prop: 'visible', value: cb.checked }); };
    rowKV('visible', cb);
  }
  if ('alpha' in o) {
    rowKV('alpha', numInput(o.alpha, function(nv){ dbg('set', { id: o.id, prop: 'alpha', value: nv }); }));
  }
  rowKV('origin', vecEditor('origin', o.origin, 3));
  if ('scale' in o) rowKV('scale', vecEditor('scale', o.scale, 3));
  if ('angle' in o) rowKV('angle (z)°', numInput(o.angle, function(nv){ dbg('set', { id: o.id, prop: 'angle', value: nv }); }));

  // ── Lecture seule ──
  if ('size' in o) rowKV('size', fmtVec(o.size));
  if ('text' in o) rowKV('text', '<pre style="margin:0;white-space:pre-wrap">' + escapeHtml(o.text) + '</pre>');
  if ('scripted' in o) rowKV('scripted', o.scripted ? 'oui' : 'non');
  if (o.model) rowKV('model', escapeHtml(o.model));
  if (o.effects && o.effects.length) {
    rowKV('effects', o.effects.map(function(e){ return '<span class="tag">' + escapeHtml(e) + '</span>'; }).join(''));
  }
}

$('#refresh').onclick = fetchScene;
$('#output').onchange = function(){ state.sel = null; state.hidden = {}; state.isolated = null; fetchScene(); };
$('#auto').onchange = function(e){
  if (state.autoTimer) { clearInterval(state.autoTimer); state.autoTimer = null; }
  if (e.target.checked) state.autoTimer = setInterval(fetchScene, 1000);
};
$('#reset').onclick = function(){
  state.hidden = {}; state.isolated = null;
  dbg('reset', {}).then(function(){ renderTree(); });
};

log('inspecteur prêt');
loadOutputs().then(function(outs){ if (outs.length) fetchScene(); });
</script>
</body>
</html>`
