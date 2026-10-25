package ui

// phonePage is the whole phone UI: no build step, no assets, one file.
const phonePage = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no,viewport-fit=cover">
<meta name="theme-color" content="#0b0d12">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<title>teveus</title>
<style>
:root { color-scheme:dark; --bg:#0b0d12; --card:#14171e; --edge:#1f2430; --text:#e9ebf2;
  --dim:#7f8798; --faint:#5a6376; --accent:#8ab4ff; --ok:#3fbf7f; --warn:#e0b341; --bad:#e0647a;
  --mono:ui-monospace,SFMono-Regular,Menlo,monospace }
* { box-sizing:border-box; -webkit-text-size-adjust:100%; -webkit-tap-highlight-color:transparent }
/* iOS zooms on a double tap and pinches even with user-scalable=no, and zooms
   into a field whose text is under 16px, so the page holds one scale. */
html { touch-action:pan-x pan-y }
body { margin:0; background:var(--bg); color:var(--text);
  font:15px/1.5 -apple-system,system-ui,sans-serif; padding-bottom:calc(84px + env(safe-area-inset-bottom)) }

/* header: one line, the state of the window at a glance */
header { position:sticky; top:0; z-index:6; display:flex; align-items:center; gap:6px;
  padding:calc(8px + env(safe-area-inset-top)) 12px 8px; background:rgba(11,13,18,.92);
  border-bottom:1px solid var(--edge); backdrop-filter:blur(14px) }
header .mark { color:var(--accent); font-weight:650; font-size:14px; letter-spacing:.01em; flex:none }
header .grow { flex:1 1 auto; min-width:0; text-align:right; font:11px/1.35 var(--mono);
  color:var(--faint); overflow:hidden; text-overflow:ellipsis; white-space:nowrap }
.chip { flex:none; border:1px solid var(--edge); background:var(--card); color:var(--dim);
  border-radius:8px; padding:3px 7px; font:11px/1.4 var(--mono); max-width:36vw;
  overflow:hidden; text-overflow:ellipsis; white-space:nowrap; cursor:pointer }
.chip:active { background:#1b2029 }
.chip.warn { color:var(--warn); border-color:#4a3d1c; background:#1d1a10 }
.chip.busy { color:var(--accent); border-color:#26364f }
#dot { width:6px; height:6px; border-radius:50%; background:var(--ok); flex:none }
#dot.busy { background:var(--warn); animation:pulse 1.1s infinite }
#dot.off { background:var(--bad) }
@keyframes pulse { 50% { opacity:.3 } }

/* transcript: quiet, tight, readable */
main { padding:14px 14px 6px; display:flex; flex-direction:column; gap:6px }
.m { white-space:pre-wrap; word-break:break-word; max-width:100% }
.m.you { align-self:flex-end; background:#18243a; border-radius:14px 14px 4px 14px;
  padding:8px 12px; max-width:88% }
.m.ai { padding:2px 1px; line-height:1.6 }
.m.tool { font:12px/1.5 var(--mono); color:var(--faint); border-left:1px solid var(--edge);
  padding:1px 0 1px 9px; overflow:hidden }
.m.tool.done { border-left-color:#2b3f33 }
.m.tool.running { border-left-color:var(--warn); color:#b7a887 }
.m.tool.failed { border-left-color:var(--bad); color:#c9909c }
.m.info { font:12px/1.5 var(--mono); color:var(--faint); text-align:center; padding:3px 8px }
.m.error { background:#1e1216; border-left:2px solid var(--bad); color:#eeb1bd; padding:7px 11px;
  border-radius:0 8px 8px 0 }
.hint { color:var(--faint); text-align:center; font-size:13px; padding:30px 16px }
.hint b { color:var(--dim); font-weight:600 }

/* approval card: impossible to miss, easy to answer one-handed */
#ask { position:fixed; left:0; right:0; z-index:9; margin:0 10px;
  bottom:calc(74px + env(safe-area-inset-bottom));
  background:linear-gradient(#231d11,#1b1810); border:1px solid #4a3d1c; border-radius:14px;
  padding:11px 12px; box-shadow:0 12px 34px rgba(0,0,0,.6) }
#ask[hidden] { display:none }
#ask h2 { margin:0 0 4px; font:600 13px/1.4 var(--mono); color:var(--warn) }
#ask .why { font-size:12px; color:#a9b0c0; margin-bottom:5px }
#ask pre { margin:0 0 9px; white-space:pre-wrap; word-break:break-word; font:12px/1.5 var(--mono);
  color:#d3d8e4; max-height:26vh; overflow:auto }
#ask .note { font:11px/1.4 var(--mono); color:var(--faint); margin-top:7px }
.row { display:flex; gap:6px }
.row button { flex:1 }
body.asking { padding-bottom:calc(270px + env(safe-area-inset-bottom)) }

button { font:inherit; border-radius:10px; border:1px solid var(--edge); background:var(--card);
  color:var(--text); padding:10px 12px; min-height:40px; cursor:pointer }
button:active { transform:scale(.985) }
.go { background:#26603f; border-color:#2f7d4f; color:#eafff2; font-weight:600 }
.no { background:#2a1a22; border-color:#5d2c39; color:#f0c3cc }

/* composer */
footer { position:fixed; left:0; right:0; bottom:0; z-index:10; display:flex; flex-direction:column;
  gap:6px; padding:7px 10px calc(9px + env(safe-area-inset-bottom));
  background:rgba(11,13,18,.96); border-top:1px solid var(--edge);
  backdrop-filter:blur(14px) }
.composer { display:flex; gap:7px; align-items:flex-end }
/* Messages sent while a turn is running: they go next, in order. */
#queue:empty { display:none }
#queue div { font:12px/1.45 var(--mono); color:var(--faint); white-space:nowrap;
  overflow:hidden; text-overflow:ellipsis; padding-left:2px }
#queue .qh { color:var(--warn) }
/* 16px is the threshold: below it iOS zooms the page when the field gets focus. */
textarea { flex:1; min-height:42px; max-height:36vh; resize:none; font:16px/1.45 inherit;
  padding:11px 12px; border-radius:11px; border:1px solid var(--edge); background:var(--card);
  color:var(--text) }
textarea:focus { outline:none; border-color:#2f4a70 }
#slash, #stop, #insert { min-width:42px; padding:0; font-size:16px; color:var(--dim); flex:none }
#stop { color:var(--bad) }
#stop[hidden], #insert[hidden] { display:none }
#insert { color:var(--warn); border-color:#4a3d1c; background:#1d1a10 }
#send { flex:none; padding:0 15px; min-height:42px }

/* sheets: modes, sessions, commands */
#modes, #sessions, #cmds, #popsheet { position:fixed; inset:0; z-index:20; background:rgba(0,0,0,.6);
  display:flex; align-items:flex-end; padding:10px 10px calc(10px + env(safe-area-inset-bottom)) }
#modes[hidden], #sessions[hidden], #cmds[hidden], #popsheet[hidden] { display:none }
.sheet { width:100%; background:#12151c; border:1px solid var(--edge); border-radius:16px;
  padding:6px; max-height:64vh; overflow:auto }
.sheet button { display:block; width:100%; text-align:left; border:0; background:transparent;
  padding:10px 11px; border-radius:10px; min-height:0 }
.sheet button:active { background:#1b2029 }
.sheet .lbl { display:block; font:13px/1.4 var(--mono); font-weight:600 }
.sheet .sub { display:block; font-size:11.5px; color:var(--faint) }
.sheet .tick { float:right; color:var(--accent) }
.sheet .grouphdr { font:11px/1.4 var(--mono); color:var(--faint); padding:8px 11px 2px }
</style></head><body>
<header>
  <span id="dot"></span><span class="mark">teveus</span>
  <button class="chip" id="sessbtn" type="button" title="Sessions"></button>
  <button class="chip" id="modebtn" type="button" title="Permission mode"></button>
  <span class="grow" id="meta"></span>
</header>
<main id="chat"><div class="hint" id="hint"><b>Nothing yet.</b><br>Type a message, or <b>/</b> for commands.</div></main>

<div id="ask" hidden><h2 id="asktitle"></h2><div class="why" id="askwhy" hidden></div>
  <pre id="askbody"></pre>
  <div class="row"><button class="no" data-a="deny">Deny</button>
    <button class="go" data-a="allow">Allow</button>
    <button id="always" data-a="always" hidden></button></div>
  <div class="note" id="asknote" hidden></div></div>

<div id="popsheet" hidden><div class="sheet">
  <div class="grouphdr" id="pophead"></div>
  <div id="poppick"></div>
  <div id="popinput" hidden><textarea id="poptext" rows="1" placeholder="Type here…"></textarea>
    <div class="row" style="margin-top:6px"><button class="go" id="popgo">OK</button></div></div>
</div></div>

<footer>
  <div id="queue"></div>
  <div class="composer">
    <button id="slash" type="button" title="Commands">/</button>
    <textarea id="msg" placeholder="Message…" rows="1"></textarea>
    <button id="insert" type="button" title="Say this when the current work ends" hidden>⏎+</button>
    <button id="stop" hidden type="button" title="Stop">■</button>
    <button class="go" id="send" type="button">↑</button>
  </div>
</footer>

<div id="modes" hidden><div class="sheet" id="modelist"></div></div>
<div id="sessions" hidden><div class="sheet" id="sessionlist"></div></div>
<div id="cmds" hidden><div class="sheet" id="cmdlist"></div></div>
<script>
const tok = location.pathname.replace(/^\/p\//, '') || new URLSearchParams(location.search).get('t') || '';
const q = p => p + '?t=' + encodeURIComponent(tok);
const post = (p, b) => fetch(q(p), {method:'POST', headers:{'Content-Type':'application/json'},
  body: JSON.stringify(b || {})});
const $ = id => document.getElementById(id);
const chat = $('chat'), hint = $('hint'), meta = $('meta'), dot = $('dot');
const ask = $('ask'), asktitle = $('asktitle'), askwhy = $('askwhy'), askbody = $('askbody');
const asknote = $('asknote'), always = $('always');
const modebtn = $('modebtn'), modesEl = $('modes'), modeList = $('modelist');
const sessbtn = $('sessbtn'), sessEl = $('sessions'), sessList = $('sessionlist');
const cmdsEl = $('cmds'), cmdList = $('cmdlist');
const popsEl = $('popsheet'), popHead = $('pophead'), popPick = $('poppick');
const popInput = $('popinput'), popText = $('poptext'), popGo = $('popgo');
const queueEl = $('queue');
const ta = $('msg'), stop = $('stop'), slashBtn = $('slash'), insertBtn = $('insert');
let modes = [], tabs = [], cur = 0, cmds = [], known = '';
let popKey = '';

// Follow the newest message unless the reader scrolled up to read.
let follow = true;
addEventListener('scroll', () => {
  follow = innerHeight + scrollY >= document.body.scrollHeight - 140;
}, {passive:true});
let firstPaint = true;
const toBottom = () => scrollTo({top: document.body.scrollHeight, behavior: firstPaint ? 'auto' : 'smooth'});

function render(v) {
  meta.textContent = [v.where, v.model].filter(Boolean).join('  ');
  dot.className = v.busy ? 'busy' : '';

  modebtn.textContent = v.mode || 'mode';
  modebtn.className = 'chip' + (v.mode && v.mode !== 'ask first' ? ' busy' : '');
  modes = v.modes || [];

  tabs = v.sessions || []; cur = v.cur || 0; cmds = v.cmds || [];
  const now = tabs[cur];
  sessbtn.textContent = (now ? now.title : 'session') + (tabs.length > 1 ? ' +' + (tabs.length - 1) : '');
  sessbtn.className = 'chip' + (tabs.some(t => t.state === 'approval') ? ' warn'
    : tabs.some(t => t.state === 'working') ? ' busy' : '');

  stop.hidden = !v.busy;
  // While it works, sending adds the message to the queue rather than
  // interrupting, so say so on the button.
  insertBtn.hidden = !v.busy;
  ta.placeholder = v.busy ? 'Queue a message…' : 'Message…';
  showQueue(v.queue || []);

  // Rebuilding the list on every poll would fight the scroll position, so
  // only touch the DOM when the messages actually changed.
  const key = JSON.stringify(v.msgs || []);
  if (key !== known) {
    known = key;
    const keep = scrollY;
    chat.textContent = '';
    (v.msgs || []).forEach(m => chat.appendChild(node(m)));
    if (!(v.msgs || []).length) chat.appendChild(hint);
    if (follow) { toBottom(); firstPaint = false } else scrollTo({top: keep});
  }

  if (v.perm) {
    ask.hidden = false; document.body.classList.add('asking');
    asktitle.textContent = 'approve ' + v.perm.tool + (v.perm.more ? ' +' + v.perm.more : '');
    askwhy.textContent = v.perm.reason || ''; askwhy.hidden = !v.perm.reason;
    askbody.textContent = v.perm.detail || '';
    always.hidden = !v.perm.always;
    always.textContent = v.perm.alwaysLabel || 'Always';
    asknote.textContent = v.perm.note || ''; asknote.hidden = !v.perm.note;
    ask.dataset.id = v.perm.id;
  } else {
    ask.hidden = true; document.body.classList.remove('asking');
  }

  showPopup(v.prompt);
}

// showQueue lists what was sent while a turn was running: it runs when the
// turn ends, in order, so a message sent mid-work isn't lost.
function showQueue(q) {
  const key = JSON.stringify(q);
  if (queueEl.dataset.key === key) return;
  queueEl.dataset.key = key;
  queueEl.textContent = '';
  q.slice(0, 3).forEach((text, i) => {
    const d = document.createElement('div');
    d.textContent = (i === 0 ? 'next: ' : 'then: ') + text.replace(/\s+/g, ' ').slice(0, 90);
    queueEl.appendChild(d);
  });
  if (q.length > 3) {
    const d = document.createElement('div');
    d.textContent = '… ' + (q.length - 3) + ' more queued';
    queueEl.appendChild(d);
  }
}

// showPopup draws the sub-window the app has open — /model's list, /effort's
// slider, an API-key field — because a phone can't reach the terminal's.
function showPopup(p) {
  // Rebuilding on every poll would swallow a keystroke in the text field.
  const key = p ? JSON.stringify(p) : '';
  if (key === popKey) return;
  popKey = key;
  if (!p) { popsEl.hidden = true; return }

  popHead.textContent = (p.title || 'choose') + (p.hint ? ' · ' + p.hint : '');
  popPick.textContent = '';
  popInput.hidden = p.kind !== 'input';

  if (p.kind === 'input') {
    popText.value = p.query || '';
    popText.type = p.masked ? 'password' : 'text';
    popsEl.hidden = false;
    return;
  }

  (p.opts || []).forEach((o, i) => {
    const b = document.createElement('button');
    b.className = i === p.sel ? 'now' : '';
    b.innerHTML = '<span class="lbl"></span><span class="sub"></span>';
    b.querySelector('.lbl').textContent =
      (p.kind === 'slider' ? (i === p.sel ? '● ' : '○ ') : '') + o.label + (i === p.sel && p.kind !== 'slider' ? ' ✓' : '');
    b.querySelector('.sub').textContent = o.held ? 'wait a moment…'
      : [o.desc, o.key].filter(Boolean).join(' · ');
    b.disabled = !!o.held;
    b.onclick = () => post('/popup', {index: i}).then(poll);
    popPick.appendChild(b);
  });
  popsEl.hidden = false;
}

popGo.onclick = () => post('/popup', {text: popText.value}).then(poll);

function node(m) {
  const d = document.createElement('div');
  d.className = 'm ' + m.role + (m.state ? ' ' + m.state : '');
  const mark = m.role !== 'tool' ? '' : m.state === 'failed' ? '✗ ' : m.state === 'running' ? '… ' : '✓ ';
  d.textContent = mark + m.text;
  return d;
}

async function poll() {
  try { render(await (await fetch(q('/state'))).json()) }
  catch (e) { dot.className = 'off'; meta.textContent = 'disconnected' }
}

/* actions */
ask.querySelectorAll('button').forEach(b => b.onclick = async () => {
  ask.querySelectorAll('button').forEach(x => x.disabled = true);
  await post('/perm', {action: b.dataset.a, id: ask.dataset.id});
  poll();
});

async function send() {
  const text = ta.value.trim(); if (!text) return;
  ta.value = ''; ta.style.height = 'auto'; follow = true;
  // While a turn runs the app queues this and sends it when the turn ends,
  // so the reply isn't cut off; the queue line below shows it waiting.
  await post('/prompt', {text}); poll();
}
$('send').onclick = send;
insertBtn.onclick = send;
slashBtn.onclick = () => { ta.value = '/'; ta.focus(); showCmds('') };
ta.addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
});
ta.addEventListener('input', () => {
  ta.style.height = 'auto';
  ta.style.height = Math.min(ta.scrollHeight, innerHeight * .36) + 'px';
  const v = ta.value;
  if (v.startsWith('/') && !v.includes('\n')) showCmds(v.slice(1)); else cmdsEl.hidden = true;
});
stop.onclick = () => post('/stop').then(poll);

/* sheets */
function fill(list, rows) {
  list.textContent = '';
  rows.forEach(r => list.appendChild(r));
}

function showCmds(filter) {
  const f = filter.trim().toLowerCase();
  // Prefix matches first: typing "/mo" should offer "model" before anything
  // whose description merely mentions a model.
  const hits = cmds.filter(c => c.name.startsWith(f))
    .concat(cmds.filter(c => !c.name.startsWith(f) && c.desc.toLowerCase().includes(f)));
  if (!hits.length) { cmdsEl.hidden = true; return }
  fill(cmdList, hits.map(c => {
    const b = document.createElement('button');
    b.innerHTML = '<span class="lbl"></span><span class="sub"></span>';
    b.querySelector('.lbl').textContent = '/' + c.name;
    b.querySelector('.sub').textContent = c.desc;
    // Fill the box and run it: the same path a typed command takes.
    b.onclick = async () => { cmdsEl.hidden = true; ta.value = ''; await post('/prompt', {text: '/' + c.name}); poll() };
    return b;
  }));
  cmdsEl.hidden = false;
}

modebtn.onclick = () => {
  fill(modeList, modes.map(md => {
    const b = document.createElement('button');
    b.className = md.now ? 'now' : '';
    b.innerHTML = '<span class="lbl"></span><span class="sub"></span>';
    b.querySelector('.lbl').textContent = md.label + (md.now ? ' ✓' : '');
    b.querySelector('.sub').textContent = md.help;
    b.onclick = async () => { modesEl.hidden = true; await post('/mode', {mode: md.id}); poll() };
    return b;
  }));
  modesEl.hidden = false;
};

sessbtn.onclick = () => {
  const rows = [];
  const mine = tabs.filter(t => !t.win);
  const theirs = tabs.filter(t => t.win);
  const row = (tb, other) => {
    const b = document.createElement('button');
    b.innerHTML = '<span class="lbl"></span><span class="sub"></span>';
    b.querySelector('.lbl').textContent = tb.title + (tb.now ? ' ✓' : '');
    const state = tb.state === 'approval' ? 'waiting for approval'
      : tb.state === 'working' ? 'working…' : 'idle';
    b.querySelector('.sub').textContent = state + (other ? ' · other window' : '');
    b.onclick = () => {
      sessEl.hidden = true;
      // Another window's page is its own: open it rather than act for it.
      if (other) { location.href = tb.url; return }
      post('/session', {id: tb.id}).then(() => { follow = true; poll() });
    };
    return b;
  };
  if (theirs.length) {
    const h = document.createElement('div'); h.className = 'grouphdr'; h.textContent = 'THIS WINDOW'; rows.push(h);
  }
  mine.forEach(tb => rows.push(row(tb, false)));
  if (theirs.length) {
    const h = document.createElement('div'); h.className = 'grouphdr'; h.textContent = 'OTHER WINDOWS'; rows.push(h);
    theirs.forEach(tb => rows.push(row(tb, true)));
  }
  fill(sessList, rows);
  sessEl.hidden = false;
};

[modesEl, sessEl, cmdsEl, popsEl].forEach(el => el.onclick = e => {
  if (e.target === el) el.hidden = true;
});

/* One fixed scale. iOS ignores user-scalable=no for pinch, and zooms on a
   double tap, so both are refused here; scrolling and tapping still work. */
addEventListener('gesturestart', e => e.preventDefault());
addEventListener('gesturechange', e => e.preventDefault());
addEventListener('gestureend', e => e.preventDefault());
let lastTap = 0;
addEventListener('touchend', e => {
  const now = Date.now();
  if (now - lastTap < 320) e.preventDefault();
  lastTap = now;
}, {passive:false});
addEventListener('dblclick', e => e.preventDefault());

addEventListener('resize', poll);
poll(); setInterval(poll, 1200);
</script></body></html>
`
