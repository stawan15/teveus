package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

// phoneMsg asks the UI to do something a phone asked for. It travels through
// the Bubble Tea loop, so the phone never touches the session directly.
type phoneMsg struct {
	text      string // a prompt to send
	perm      string // "allow", "always" or "deny" for the question on screen
	request   string // the permission request the answer belongs to
	interrupt bool
}

// phoneView is what the phone's page shows: a plain-text transcript and the
// question waiting for an answer. The UI fills it in on every frame.
type phoneView struct {
	Transcript string     `json:"transcript"`
	Busy       bool       `json:"busy"`
	Phase      string     `json:"phase"`
	Perm       *phonePerm `json:"perm"`
	Empty      bool       `json:"empty"`
}

type phonePerm struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Detail    string `json:"detail"`
	Always    bool   `json:"always"`
	AlwaysLbl string `json:"alwaysLabel"`
	More      int    `json:"more"`
}

// phoneServer serves the phone page and the endpoints the page calls.
type phoneServer struct {
	token string
	addr  string
	url   string
	srv   *http.Server
	send  func(tea.Msg)

	mu   sync.Mutex
	view phoneView
}

// baseURL is where the page is served, without the token.
func (p *phoneServer) baseURL() string {
	return "http://" + p.addr
}

// phoneAddr is where the page listens once the user turns it on. It has to
// answer the phone, not just this machine, so it listens on every interface;
// the token in the link is what keeps other people out.
const phoneAddr = ":8787"

// phoneHost is the address to put in the link: the machine's own address on
// the local network, or the loopback when there isn't one.
func phoneHost(port string) string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			ip, ok := a.(*net.IPNet)
			if !ok || ip.IP.IsLoopback() || ip.IP.To4() == nil {
				continue
			}
			return net.JoinHostPort(ip.IP.String(), port)
		}
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// startPhone listens on addr and returns the URL to open. send hands messages
// to the UI's loop.
func (m *Model) startPhone(addr string, send func(tea.Msg)) (string, error) {
	if m.phone != nil {
		return m.phone.url, nil
	}
	if addr == "" {
		addr = phoneAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		ln.Close()
		return "", err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p := &phoneServer{token: hex.EncodeToString(b[:]), addr: net.JoinHostPort("127.0.0.1", port), send: send}
	p.url = "http://" + phoneHost(port) + "/?t=" + p.token

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.page)
	mux.HandleFunc("/state", p.state)
	mux.HandleFunc("/prompt", p.prompt)
	mux.HandleFunc("/perm", p.answer)
	mux.HandleFunc("/stop", p.stop)
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go p.srv.Serve(ln)
	m.phone = p
	return p.url, nil
}

func (m *Model) stopPhone() {
	if m.phone == nil {
		return
	}
	m.phone.srv.Close()
	m.phone = nil
}

// phoneOn reports whether the phone page is serving now.
func (m *Model) phoneOn() bool { return m.phone != nil }

// togglePhone turns the phone page on or off from the settings list.
func (m *Model) togglePhone() tea.Cmd {
	if m.phone != nil {
		m.stopPhone()
		m.settings.PhonePage = false
		saveSettings(m.settings)
		m.note("phone page off", true)
		return nil
	}
	m.settings.PhonePage = true
	saveSettings(m.settings)
	if m.cfg.Send == nil {
		m.note("the phone page needs the full app to run", false)
		return nil
	}
	url, err := m.startPhone(phoneAddr, m.cfg.Send)
	if err != nil {
		m.note("phone page: "+err.Error(), false)
		return nil
	}
	m.add(&block{kind: kindInfo, text: "Phone: open " + url + " on a device that can reach this machine. Anyone with the link can prompt and approve, so keep it to yourself."})
	m.note("phone page on", true)
	return nil
}

// snapshot copies what the page shows. It runs on the UI's own goroutine, so
// it can read the model; the page reads the copy under a lock.
func (p *phoneServer) snapshot(m *Model) {
	v := phoneView{Busy: m.busy, Phase: m.phase, Empty: len(m.blocks) == 0}
	var sb strings.Builder
	for _, b := range m.blocks {
		line := phoneBlock(b, m)
		if line == "" {
			continue
		}
		sb.WriteString(line)
		sb.WriteString("\n\n")
	}
	v.Transcript = strings.TrimSpace(sb.String())
	if len(m.perms) > 0 {
		req := m.perms[0]
		v.Perm = &phonePerm{ID: req.RequestID, Tool: displayName(req.ToolName), Detail: phoneDetail(req),
			Always: hasSuggestions(req), AlwaysLbl: req.AlwaysLabel, More: len(m.perms) - 1}
	}
	p.mu.Lock()
	p.view = v
	p.mu.Unlock()
}

// phoneBlock is one transcript entry as plain text, without colour.
func phoneBlock(b *block, m *Model) string {
	switch b.kind {
	case kindUser:
		return "You: " + b.text
	case kindAssistant:
		return termSafe(b.text)
	case kindTool:
		head := displayName(b.name)
		if s := m.r.summary(b.name, b.input); s != "" {
			head += " " + s
		}
		switch b.state {
		case toolRunning:
			head = "… " + head
		case toolFailed:
			head = "✗ " + head
		default:
			head = "✓ " + head
		}
		if b.result != "" {
			head += "\n" + clipText(termSafe(b.result), 1200)
		}
		return head
	case kindError:
		return "✗ " + b.text
	case kindInfo, kindCmdOut:
		return strings.TrimSpace(b.text)
	}
	return ""
}

// phoneDetail is the part of a permission request worth reading on a phone:
// the command, the file, or the tool's arguments.
func phoneDetail(req *claude.PermissionRequest) string {
	var in map[string]any
	json.Unmarshal(req.Input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch req.ToolName {
	case "Bash":
		return "$ " + str("command")
	case "Write", "Edit", "MultiEdit", "Read":
		return str("file_path")
	}
	if d := string(req.Input); d != "" {
		return clipText(d, 800)
	}
	return req.Description
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…"
}

func (p *phoneServer) allowed(r *http.Request) bool {
	t := r.URL.Query().Get("t")
	if t == "" {
		t = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(t), []byte(p.token)) == 1
}

func (p *phoneServer) state(w http.ResponseWriter, r *http.Request) {
	if !p.allowed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	v := p.view
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (p *phoneServer) decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if !p.allowed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

func (p *phoneServer) prompt(w http.ResponseWriter, r *http.Request) {
	var body struct{ Text string }
	if !p.decode(w, r, &body) {
		return
	}
	if t := strings.TrimSpace(body.Text); t != "" {
		p.send(phoneMsg{text: t})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *phoneServer) answer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string // allow, always, deny
		ID     string // the request the page was showing
	}
	if !p.decode(w, r, &body) {
		return
	}
	switch body.Action {
	case "allow", "always", "deny":
	default:
		http.Error(w, "action must be allow, always or deny", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	v := p.view
	p.mu.Unlock()
	if v.Perm == nil || v.Perm.ID != body.ID {
		http.Error(w, "that approval was already answered", http.StatusConflict)
		return
	}
	p.send(phoneMsg{perm: body.Action, request: body.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (p *phoneServer) stop(w http.ResponseWriter, r *http.Request) {
	if !p.allowed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p.send(phoneMsg{interrupt: true})
	w.WriteHeader(http.StatusNoContent)
}

func (p *phoneServer) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !p.allowed(r) {
		http.Error(w, "This page needs the link teveus printed, with its token.", http.StatusUnauthorized)
		return
	}
	w.Write([]byte(phonePage))
}

// phonePage is the whole phone UI: no build step, no assets.
const phonePage = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#12141a">
<title>teveus</title>
<style>
:root { color-scheme: dark }
* { box-sizing: border-box }
body { margin:0; background:#12141a; color:#e6e8ee; font:16px/1.5 -apple-system,system-ui,sans-serif;
  padding:12px 12px calc(12px + env(safe-area-inset-bottom)); }
h1 { font-size:15px; margin:0 0 10px; color:#8b93a7; font-weight:600; letter-spacing:.02em }
h1 b { color:#8ab4ff }
#ask:empty, #phase[hidden], #trace:empty { display:none }
#ask { background:#1b1e27; border:1px solid #e0b341; border-radius:12px; padding:12px; margin-bottom:12px }
#ask h2 { margin:0 0 6px; font-size:15px; color:#e0b341 }
#ask pre { margin:0 0 10px; white-space:pre-wrap; word-break:break-word; font:13px/1.5 ui-monospace,monospace;
  color:#c8cede; max-height:34vh; overflow:auto }
button { font:inherit; border-radius:10px; border:1px solid #2c3140; background:#1b1e27; color:#e6e8ee;
  padding:12px 14px; min-height:46px }
button.go { background:#2f7d4f; border-color:#2f7d4f; color:#fff }
button.no { background:#3a2430; border-color:#7d3a4b }
.row { display:flex; gap:8px; flex-wrap:wrap; margin-bottom:10px }
.row button { flex:1 }
#trace { white-space:pre-wrap; word-break:break-word; font:13px/1.55 ui-monospace,monospace; color:#c8cede;
  border-top:1px solid #222634; padding-top:10px; margin-bottom:12px }
#phase { color:#8ab4ff; font-size:13px; margin-bottom:10px }
form { position:sticky; bottom:0; display:flex; gap:8px; background:#12141a; padding-top:8px }
textarea { flex:1; min-height:52px; max-height:40vh; resize:vertical; font:inherit; padding:10px;
  border-radius:10px; border:1px solid #2c3140; background:#1b1e27; color:#e6e8ee }
</style></head><body>
<h1><b>✦ teveus</b> <span id="where"></span></h1>
<div id="ask"></div>
<div id="phase" hidden></div>
<div id="trace"></div>
<form id="f"><textarea id="msg" placeholder="Message…" rows="1"></textarea>
<button class="go" type="submit">Send</button></form>
<button id="stop" hidden style="width:100%;margin-top:8px">Stop</button>
<script>
const t = new URLSearchParams(location.search).get('t') || '';
const q = p => p + '?t=' + encodeURIComponent(t);
const post = (p, body) => fetch(q(p), {method:'POST', headers:{'Content-Type':'application/json'},
  body: JSON.stringify(body || {})});
const ask = document.getElementById('ask'), trace = document.getElementById('trace');
const phase = document.getElementById('phase'), ta = document.getElementById('msg');
const stop = document.getElementById('stop');
stop.onclick = () => post('/stop').then(poll);

function perm(v) {
  if (!v) { ask.innerHTML = ''; return }
  let extra = v.always ? '<button data-a="always">' + (v.alwaysLabel || 'always allow') + '</button>' : '';
  ask.innerHTML = '<h2>Approve ' + v.tool + (v.more ? ' (+' + v.more + ' waiting)' : '') + '</h2>'
    + '<pre></pre><div class="row"><button class="go" data-a="allow">Allow</button>'
    + extra + '<button class="no" data-a="deny">Deny</button></div>';
  ask.querySelector('pre').textContent = v.detail || '';
  ask.querySelectorAll('button').forEach(b => b.onclick = async () => {
    b.disabled = true; await post('/perm', {action: b.dataset.a, id: v.id}); poll();
  });
}

async function poll() {
  try {
    const v = await (await fetch(q('/state'))).json();
    trace.textContent = v.transcript || '';
    phase.hidden = !v.busy; phase.textContent = v.busy ? (v.phase || 'working') + '…' : '';
    stop.hidden = !v.busy;
    perm(v.perm);
  } catch (e) { phase.hidden = false; phase.textContent = 'disconnected'; }
}

document.getElementById('f').onsubmit = async e => {
  e.preventDefault();
  const text = ta.value.trim(); if (!text) return;
  ta.value = ''; await post('/prompt', {text}); poll();
};
setInterval(poll, 1000); poll();
</script></body></html>
`
