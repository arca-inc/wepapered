package daemon

import "net/http"

// serveInspector serves the standalone scene debug inspector — a self-contained
// page (no WE/Angular dependency) that talks to the daemon's /inspect JSON API:
// it lists introspectable monitors, fetches the live scene graph, and renders an
// object tree + property panel + a raw-JSON console. Read-only for now (Phase 3);
// isolate/hide commands (objectFilter/skipObjects) come later.
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
  label.chk { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); cursor: pointer; }
  main { flex: 1; display: flex; min-height: 0; }
  #tree { width: 42%; overflow: auto; border-right: 1px solid var(--line); padding: 6px 0; }
  #side { flex: 1; display: flex; flex-direction: column; min-width: 0; }
  #props { flex: 1; overflow: auto; padding: 12px; }
  #console {
    flex: 0 0 30%; overflow: auto; border-top: 1px solid var(--line);
    background: #16171b; padding: 8px 12px; font-family: var(--mono); font-size: 12px; white-space: pre-wrap;
  }
  .row {
    display: flex; align-items: center; gap: 6px; padding: 3px 8px; cursor: pointer;
    white-space: nowrap; user-select: none;
  }
  .row:hover { background: var(--panel2); }
  .row.sel { background: #34537a; }
  .row.hidden .name { color: var(--hidden); text-decoration: line-through; }
  .tw { width: 14px; text-align: center; color: var(--muted); flex: 0 0 auto; }
  .badge {
    font-size: 10px; padding: 1px 6px; border-radius: 4px; background: var(--line);
    color: var(--muted); flex: 0 0 auto; text-transform: uppercase; letter-spacing: .04em;
  }
  .badge.compose { background: #5a3a7a; color: #e6d0ff; }
  .badge.image { background: #2f5a45; color: #c8f0d8; }
  .badge.text { background: #5a4a2f; color: #ffe6c0; }
  .badge.particle { background: #2f475a; color: #c0e6ff; }
  .badge.sound { background: #5a2f3a; color: #ffc8d4; }
  .name { overflow: hidden; text-overflow: ellipsis; }
  .id { color: var(--muted); font-family: var(--mono); font-size: 11px; }
  #props h2 { margin: 0 0 4px; font-size: 15px; }
  #props .sub { color: var(--muted); margin-bottom: 14px; font-family: var(--mono); font-size: 12px; }
  table.kv { border-collapse: collapse; width: 100%; }
  table.kv td { padding: 4px 8px; border-bottom: 1px solid var(--line); vertical-align: top; }
  table.kv td.k { color: var(--muted); width: 130px; }
  table.kv td.v { font-family: var(--mono); }
  .empty { color: var(--muted); padding: 24px; text-align: center; }
  .tag { display: inline-block; background: var(--panel2); border: 1px solid var(--line); border-radius: 4px; padding: 1px 6px; margin: 2px 4px 2px 0; font-family: var(--mono); font-size: 11px; }
  .clog-err { color: #ff8080; }
  .clog-ok { color: #80d890; }
</style>
</head>
<body>
<header>
  <h1>🔍 Inspecteur de scène</h1>
  <select id="output" title="Moniteur"></select>
  <button id="refresh">↻ Rafraîchir</button>
  <label class="chk"><input type="checkbox" id="auto"> auto (1s)</label>
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
var state = { data: null, sel: null, autoTimer: null };

function log(msg, cls) {
  var c = $('#console');
  var line = '[' + new Date().toLocaleTimeString() + '] ' + msg + '\n';
  var span = document.createElement('span');
  if (cls) span.className = cls;
  span.textContent = line;
  c.appendChild(span);
  c.scrollTop = c.scrollHeight;
}

function api(path) {
  return fetch(path, { cache: 'no-store' }).then(function(r){
    if (!r.ok) return r.text().then(function(t){ throw new Error(r.status + ' ' + t); });
    return r.json();
  });
}

function loadOutputs() {
  return api('/inspect').then(function(j){
    var sel = $('#output');
    var prev = sel.value;
    sel.innerHTML = '';
    (j.outputs || []).forEach(function(name){
      var o = document.createElement('option');
      o.value = name; o.textContent = name;
      sel.appendChild(o);
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
    log('scène chargée : ' + n + ' objets', 'clog-ok');
  }).catch(function(e){
    $('#stat').textContent = 'erreur';
    log('inspect: ' + e.message, 'clog-err');
  });
}

function findObj(id) {
  return (state.data.objects || []).filter(function(o){ return o.id === id; })[0] || null;
}

// Build a parent→children index and render the object graph as a tree, falling
// back to a flat list for orphans / cycles.
function renderTree() {
  var objs = state.data.objects || [];
  var byId = {}; objs.forEach(function(o){ byId[o.id] = o; });
  var children = {}; var roots = [];
  objs.forEach(function(o){
    var p = o.parent;
    if (p != null && byId[p]) { (children[p] = children[p] || []).push(o); }
    else { roots.push(o); }
  });
  var tree = $('#tree');
  tree.innerHTML = '';
  var seen = {};
  function emit(o, depth) {
    if (seen[o.id]) return; seen[o.id] = true;
    tree.appendChild(rowFor(o, depth));
    (children[o.id] || []).forEach(function(c){ emit(c, depth + 1); });
  }
  roots.forEach(function(o){ emit(o, 0); });
  // any object not reachable (cycle) appended flat
  objs.forEach(function(o){ if (!seen[o.id]) emit(o, 0); });
}

function rowFor(o, depth) {
  var div = document.createElement('div');
  div.className = 'row' + (state.sel === o.id ? ' sel' : '') + (o.visible === false ? ' hidden' : '');
  div.style.paddingLeft = (8 + depth * 16) + 'px';
  var tw = document.createElement('span'); tw.className = 'tw'; tw.textContent = '•';
  var badge = document.createElement('span'); badge.className = 'badge ' + (o.type || 'object'); badge.textContent = o.type || 'obj';
  var name = document.createElement('span'); name.className = 'name'; name.textContent = o.name || '(sans nom)';
  var id = document.createElement('span'); id.className = 'id'; id.textContent = '#' + o.id;
  div.appendChild(tw); div.appendChild(badge); div.appendChild(name); div.appendChild(id);
  div.onclick = function(){ state.sel = o.id; renderTree(); renderProps(o); };
  return div;
}

function fmtVec(v) {
  if (!Array.isArray(v)) return String(v);
  return '[' + v.map(function(n){ return (typeof n === 'number') ? +n.toFixed(2) : n; }).join(', ') + ']';
}

function renderProps(o) {
  var p = $('#props');
  if (!o) { p.innerHTML = '<div class="empty">Sélectionne un objet.</div>'; return; }
  var rows = '';
  function kv(k, v) { rows += '<tr><td class="k">' + k + '</td><td class="v">' + v + '</td></tr>'; }
  kv('id', o.id);
  kv('type', o.type);
  kv('parent', o.parent == null ? '—' : '#' + o.parent);
  kv('visible', o.visible === false ? '<span class="clog-err">non</span>' : 'oui');
  if ('alpha' in o) kv('alpha', (+o.alpha).toFixed(3));
  kv('origin', fmtVec(o.origin));
  if ('scale' in o) kv('scale', fmtVec(o.scale));
  if ('angle' in o) kv('angle (z)', (+o.angle).toFixed(2) + '°');
  if ('size' in o) kv('size', fmtVec(o.size));
  if ('text' in o) kv('text', '<pre style="margin:0;white-space:pre-wrap">' + escapeHtml(o.text) + '</pre>');
  if ('scripted' in o) kv('scripted', o.scripted ? 'oui' : 'non');
  if (o.model) kv('model', escapeHtml(o.model));
  if (o.effects && o.effects.length) {
    kv('effects', o.effects.map(function(e){ return '<span class="tag">' + escapeHtml(e) + '</span>'; }).join(''));
  }
  p.innerHTML = '<h2>' + escapeHtml(o.name || '(sans nom)') + '</h2>' +
    '<div class="sub">' + (o.type || 'object') + ' · #' + o.id + '</div>' +
    '<table class="kv">' + rows + '</table>';
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"]/g, function(c){
    return { '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;' }[c];
  });
}

$('#refresh').onclick = fetchScene;
$('#output').onchange = function(){ state.sel = null; fetchScene(); };
$('#auto').onchange = function(e){
  if (state.autoTimer) { clearInterval(state.autoTimer); state.autoTimer = null; }
  if (e.target.checked) state.autoTimer = setInterval(fetchScene, 1000);
};

log('inspecteur prêt');
loadOutputs().then(function(outs){ if (outs.length) fetchScene(); });
</script>
</body>
</html>`
