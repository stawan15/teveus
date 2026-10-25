package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	mode      string // a permission mode to switch to
	session   *int   // a session to switch to
	interrupt bool
}

// phoneView is what the phone's page shows: the conversation as plain lines,
// and the question waiting for an answer. The UI fills it in on every frame.
type phoneView struct {
	Msgs     []phoneLine `json:"msgs"`
	Busy     bool        `json:"busy"`
	Phase    string      `json:"phase"`
	Perm     *phonePerm  `json:"perm"`
	Where    string      `json:"where"` // folder name, for the header
	Model    string      `json:"model"`
	Mode     string      `json:"mode"` // permission mode, as the sidebar names it
	Modes    []phoneMode `json:"modes"`
	Sessions []phoneTab  `json:"sessions"`
	Cur      int         `json:"cur"` // the session on screen
	Tab      string      `json:"tab"` // this window's session, for other windows to show
}

// phoneTab is one open session, so the phone can switch between them. A
// session in this window has an ID; one in another window has the address and
// token of that window's page instead.
type phoneTab struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	State string `json:"state"` // "idle", "working" or "approval"
	Now   bool   `json:"now"`
	Win   string `json:"win,omitempty"`   // another window: its host:port
	Tok   string `json:"tok,omitempty"`   // another window: its page token
	URL   string `json:"url,omitempty"`   // another window: its page
	Where string `json:"where,omitempty"` // another window: its folder
}

// phoneMode is one permission mode the page can switch to.
type phoneMode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Help  string `json:"help"`
	Now   bool   `json:"now"`
}

// phoneLine is one message of the conversation, ready to style on the page.
type phoneLine struct {
	Role  string `json:"role"` // "you", "ai", "tool", "info" or "error"
	Text  string `json:"text"`
	State string `json:"state"` // tools: "running", "done" or "failed"
}

type phonePerm struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Detail    string `json:"detail"`
	Always    bool   `json:"always"`
	AlwaysLbl string `json:"alwaysLabel"`
	More      int    `json:"more"`
	// Why this one came to the user, and what denying it means, so the phone
	// shows the same reasoning the terminal does.
	Reason string `json:"reason,omitempty"`
	Note   string `json:"note,omitempty"`
}

// phoneServer serves the phone page and the endpoints the page calls.
type phoneServer struct {
	token       string
	addr        string
	publicAddr  string // how a phone reaches this window, for other windows to use
	url         string
	srv         *http.Server
	send        func(tea.Msg)
	handle      string // this window's entry in the config dir, when serving
	handleTitle string

	mu     sync.Mutex
	view   phoneView
	others []phoneTab // other windows, as of the last poll
}

// baseURL is where the page is served, without the token.
func (p *phoneServer) baseURL() string {
	return "http://" + p.addr
}

// phoneToken is the token the link carries. It is kept in the config dir, so
// the link stays the same between runs and can be bookmarked on the phone.
// Deleting ~/.config/teveus/phone-token gives out a new one, which makes every
// old link stop working.
func phoneToken(fresh string) string {
	path := filepath.Join(configDir(), "phone-token")
	if t, ok := readToken(path); ok {
		return t
	}
	// Two windows can start at the same moment. Whoever creates the file
	// first wins; the other waits for the winner's token, so both pages are
	// reachable with the one link.
	os.MkdirAll(configDir(), 0o700)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		for i := 0; i < 40; i++ { // the winner may not have finished writing yet
			if t, ok := readToken(path); ok {
				return t
			}
			time.Sleep(5 * time.Millisecond)
		}
		return fresh
	}
	f.WriteString(fresh + "\n")
	f.Close()
	os.Chmod(path, 0o600)
	return fresh
}

func readToken(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	t := strings.TrimSpace(string(b))
	return t, len(t) >= 32
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
// to the UI's loop. If the port is taken (an older teveus, say), it moves on to
// the next one instead of failing: the link it prints is what the user needs.
func (m *Model) startPhone(addr string, send func(tea.Msg)) (string, error) {
	if m.phone != nil {
		return m.phone.url, nil
	}
	if addr == "" {
		addr = phoneAddr
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", err
	}
	var ln net.Listener
	var lerr error
	for i := 0; i < 8; i++ {
		ln, lerr = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+i)))
		if lerr == nil {
			break
		}
	}
	if lerr != nil {
		return "", lerr
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		ln.Close()
		return "", err
	}
	_, boundPort, _ := net.SplitHostPort(ln.Addr().String())
	p := &phoneServer{token: phoneToken(hex.EncodeToString(b[:])), addr: net.JoinHostPort("127.0.0.1", boundPort), send: send}
	// The link has to name an address the page is actually listening on: a
	// specific host as given, or this machine's own address when it listens
	// on every interface.
	linkHost := host
	if linkHost == "" || linkHost == "0.0.0.0" || linkHost == "::" {
		linkHost = phoneHost(boundPort)
	} else if linkHost == "localhost" {
		linkHost = net.JoinHostPort("127.0.0.1", boundPort)
	}
	if _, _, err := net.SplitHostPort(linkHost); err != nil {
		linkHost = net.JoinHostPort(linkHost, boundPort)
	}
	p.publicAddr = linkHost
	p.url = "http://" + linkHost + "/p/" + p.token

	mux := http.NewServeMux()
	mux.HandleFunc("/p/", p.page)
	mux.HandleFunc("/state", p.state)
	mux.HandleFunc("/prompt", p.prompt)
	mux.HandleFunc("/perm", p.answer)
	mux.HandleFunc("/mode", p.mode)
	mux.HandleFunc("/session", p.session)
	mux.HandleFunc("/stop", p.stop)
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go p.srv.Serve(ln)
	m.phone = p
	p.writeHandle(m)
	go p.pollLoop()
	return p.url, nil
}

// pollLoop keeps asking the other windows what they are doing, so the page
// can list them without waiting on another process while it draws.
func (p *phoneServer) pollLoop() {
	for range time.Tick(3 * time.Second) {
		p.pollWindows()
	}
}

func (m *Model) stopPhone() {
	if m.phone == nil {
		return
	}
	m.phone.removeHandle()
	m.phone.srv.Close()
	m.phone = nil
}

// phoneOn reports whether the phone page is serving now.
func (m *Model) phoneOn() bool { return m.phone != nil }

// showPhone is /phone: it turns the page on or off, or prints the link again.
func (m *Model) showPhone(arg string) tea.Cmd {
	switch strings.TrimSpace(arg) {
	case "off":
		if m.phone != nil {
			m.stopPhone()
		}
		m.settings.PhonePage = false
		saveSettings(m.settings)
		m.add(&block{kind: kindInfo, text: "Phone page off."})
		return nil
	case "on":
		return m.togglePhone()
	}
	if m.phone == nil {
		return m.togglePhone()
	}
	m.add(&block{kind: kindInfo, text: "Phone: open this on a device on the same network —\n" + m.phone.url})
	return nil
}

// togglePhone turns the phone page on or off from the settings list.
func (m *Model) togglePhone() tea.Cmd {
	if m.phone != nil {
		m.stopPhone()
		m.settings.PhonePage = false
		saveSettings(m.settings)
		m.note("phone page off", true)
		return nil
	}
	if m.cfg.Send == nil {
		m.add(&block{kind: kindError, text: "The phone page needs the full app: run teveus in a terminal, then turn it on with /phone."})
		return nil
	}
	url, err := m.startPhone(phoneAddr, m.cfg.Send)
	if err != nil {
		// Said in the transcript too: a status note is gone in a second, and
		// this is the line the user needs to read.
		m.add(&block{kind: kindError, text: "Phone page couldn't start on port 8787: " + err.Error() + " (another app may hold it; /phone off, then on, tries again)"})
		return nil
	}
	m.settings.PhonePage = true
	saveSettings(m.settings)
	m.add(&block{kind: kindInfo, text: "Phone: open this on a device on the same network —\n" + url +
		"\nAnyone with this link can prompt and approve. Turn it off with /phone off."})
	m.note("phone page on", true)
	return nil
}

// snapshot copies what the page shows. It runs on the UI's own goroutine, so
// it can read the model; the page reads the copy under a lock.
func (p *phoneServer) snapshot(m *Model) { p.update(m) }

// update copies the model's state for the page to read.
func (p *phoneServer) update(m *Model) {
	v := phoneView{Busy: m.busy, Phase: m.phase, Where: shortPath(m.cwd), Model: shortModel(m.model),
		Mode: modeLabel(m.mode)}
	for _, md := range modes {
		v.Modes = append(v.Modes, phoneMode{ID: md, Label: modeLabel(md), Help: modeHelp(md, m.engine), Now: md == m.mode})
	}
	for i := range m.sessions {
		title, busy, waiting := m.sessionState(i)
		state := "idle"
		switch {
		case waiting:
			state = "approval"
		case busy:
			state = "working"
		}
		v.Sessions = append(v.Sessions, phoneTab{ID: i, Title: title, State: state, Now: i == m.cur})
	}
	v.Cur = m.cur
	p.mu.Lock()
	others := p.others
	p.mu.Unlock()
	v.Sessions = append(v.Sessions, others...)
	v.Tab = sessionTitle(m.blocks)
	for _, b := range m.blocks {
		if line, ok := phoneBlock(b, m); ok {
			v.Msgs = append(v.Msgs, line)
		}
	}
	if m.phone != nil {
		m.phone.refreshHandle(m)
	}
	if len(m.perms) > 0 {
		req := m.perms[0]
		v.Perm = &phonePerm{ID: req.RequestID, Tool: displayName(req.ToolName), Detail: phoneDetail(req),
			Always: hasSuggestions(req), AlwaysLbl: req.AlwaysLabel, More: len(m.perms) - 1,
			Reason: strings.TrimSpace(req.Description), Note: phoneNote(req, m.engine)}
	}
	p.mu.Lock()
	p.view = v
	p.mu.Unlock()
}

// phoneBlock is one transcript entry as a line the page can style.
func phoneBlock(b *block, m *Model) (phoneLine, bool) {
	switch b.kind {
	case kindUser:
		return phoneLine{Role: "you", Text: termSafe(b.text)}, true
	case kindAssistant:
		if t := strings.TrimSpace(termSafe(b.text)); t != "" {
			return phoneLine{Role: "ai", Text: t}, true
		}
	case kindTool:
		head := displayName(b.name)
		if s := m.r.summary(b.name, b.input); s != "" {
			head += " " + s
		}
		state := "done"
		switch b.state {
		case toolRunning:
			state = "running"
		case toolFailed:
			state = "failed"
		}
		if b.result != "" {
			head += "\n" + clipText(termSafe(b.result), 1200)
		}
		return phoneLine{Role: "tool", Text: head, State: state}, true
	case kindError:
		return phoneLine{Role: "error", Text: b.text}, true
	case kindInfo, kindCmdOut:
		if t := strings.TrimSpace(b.text); t != "" {
			return phoneLine{Role: "info", Text: t}, true
		}
	}
	return phoneLine{}, false
}

// phoneDetail is the part of a permission request worth reading on a phone:
// what it will run, the file and its change, or the tool's arguments.
func phoneDetail(req *claude.PermissionRequest) string {
	var in map[string]any
	json.Unmarshal(req.Input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch req.ToolName {
	case "Bash":
		return "$ " + str("command")
	case "Edit", "MultiEdit":
		if d := str("diff"); d != "" {
			return clipText(d, 1600)
		}
		return str("file_path")
	case "Write":
		if c := str("content"); c != "" {
			return clipText(str("file_path")+"\n\n"+c, 1600)
		}
		return str("file_path")
	case "Read":
		return str("file_path")
	}
	if b, err := json.MarshalIndent(in, "", "  "); err == nil && len(b) > 2 {
		return clipText(termSafe(string(b)), 800)
	}
	return req.Description
}

// phoneNote says what denying does, matching what the terminal's prompt
// implies with its "deny" key.
func phoneNote(req *claude.PermissionRequest, engine string) string {
	if engine == "claude" {
		return "Deny tells Claude Code no; it then waits for your instructions."
	}
	return "Deny skips this tool; the agent then waits for your instructions."
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
	if t == "" {
		// The link carries it in the path, so it can be bookmarked and shared
		// as one URL.
		t = strings.TrimPrefix(r.URL.Path, "/p/")
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

func (p *phoneServer) mode(w http.ResponseWriter, r *http.Request) {
	var body struct{ Mode string }
	if !p.decode(w, r, &body) {
		return
	}
	for _, md := range modes {
		if md == body.Mode {
			p.send(phoneMsg{mode: md})
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, "unknown mode", http.StatusBadRequest)
}

func (p *phoneServer) session(w http.ResponseWriter, r *http.Request) {
	var body struct{ ID *int }
	if !p.decode(w, r, &body) {
		return
	}
	if body.ID == nil {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	n := len(p.view.Sessions)
	p.mu.Unlock()
	if *body.ID < 0 || *body.ID >= n {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	p.send(phoneMsg{session: body.ID})
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
<meta name="theme-color" content="#0f1116">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<title>teveus</title>
<style>
:root { color-scheme: dark; --bg:#0f1116; --card:#171b23; --edge:#242a36; --text:#e8eaf0;
  --dim:#8d95a8; --accent:#8ab4ff; --ok:#3fbf7f; --warn:#e0b341; --bad:#e0647a }
* { box-sizing:border-box; -webkit-text-size-adjust:100% }
body { margin:0; background:var(--bg); color:var(--text); font:16px/1.55 -apple-system,system-ui,sans-serif;
  padding-bottom:calc(96px + env(safe-area-inset-bottom)) }
header { position:sticky; top:0; z-index:5; display:flex; align-items:center; gap:8px;
  padding:calc(10px + env(safe-area-inset-top)) 14px 10px; background:rgba(15,17,22,.94);
  border-bottom:1px solid var(--edge); backdrop-filter:blur(12px) }
header b { color:var(--accent); font-size:15px; letter-spacing:.01em }
header .meta { margin-left:auto; text-align:right; font-size:12px; color:var(--dim); line-height:1.3;
  max-width:52%; overflow:hidden; text-overflow:ellipsis; white-space:nowrap }
#dot { width:8px; height:8px; border-radius:50%; background:var(--ok); flex:none }
#dot.busy { background:var(--warn); animation:pulse 1.1s infinite }
@keyframes pulse { 50% { opacity:.35 } }
#modebtn, #sessbtn { margin-left:4px; min-height:30px; padding:4px 10px; border-radius:999px; font-size:12px;
  background:#1d2433; border-color:#31405c; color:var(--accent); flex:none; max-width:34vw;
  overflow:hidden; text-overflow:ellipsis; white-space:nowrap }
#sessbtn { color:#c8cede }
#sessbtn.working { border-color:var(--warn); color:var(--warn) }
#sessbtn.approval { border-color:var(--warn); background:#2a2416; color:var(--warn) }
#modes, #sessions { position:fixed; inset:0; z-index:20; background:rgba(0,0,0,.55); display:flex;
  align-items:flex-end; padding:10px }
#modes[hidden], #sessions[hidden] { display:none }
.sheet { width:100%; background:#171b23; border:1px solid var(--edge); border-radius:16px; padding:8px }
.sheet button { display:block; width:100%; text-align:left; margin:0; border:0; background:transparent;
  border-radius:12px; padding:12px }
.sheet button.now { background:#1d2a44 }
.sheet .lbl { font-weight:600 }
.sheet .help { color:var(--dim); font-size:12px }
.sheet .tick { float:right; color:var(--accent) }
main { padding:12px 14px 4px; display:flex; flex-direction:column; gap:8px }
.msg { border-radius:14px; padding:10px 13px; white-space:pre-wrap; word-break:break-word }
.you { background:#1d2a44; align-self:flex-end; max-width:88%; border-bottom-right-radius:5px }
.ai { background:var(--card); border:1px solid var(--edge); max-width:94% }
.tool { font:13px/1.5 ui-monospace,SFMono-Regular,monospace; color:var(--dim);
  background:transparent; border-left:2px solid var(--edge); border-radius:0; padding:2px 0 2px 10px }
.tool.running { border-left-color:var(--warn); color:#c9b790 }
.tool.failed { border-left-color:var(--bad); color:#e5a3ae }
.info { font-size:13px; color:var(--dim); text-align:center; padding:2px 6px }
.error { background:#2a1a20; border:1px solid #4a2733; color:#f0aab8 }
.hint { color:var(--dim); text-align:center; font-size:14px; padding:24px 10px }
#ask { position:fixed; left:0; right:0; bottom:calc(76px + env(safe-area-inset-bottom)); z-index:8;
  margin:0 10px; background:#241f16; border:1px solid var(--warn); border-radius:16px;
  padding:12px 14px; box-shadow:0 10px 30px rgba(0,0,0,.5) }
#ask[hidden] { display:none }
body.asking { padding-bottom:calc(280px + env(safe-area-inset-bottom)) }
#ask h2 { margin:0 0 6px; font-size:14px; color:var(--warn); font-weight:600 }
#askwhy { font-size:13px; color:#b6bdcf; margin-bottom:6px }
#asknote { font-size:12px; color:var(--dim); margin-top:8px }
#ask pre { margin:0 0 10px; white-space:pre-wrap; word-break:break-word;
  font:13px/1.5 ui-monospace,monospace; color:#d6dbe8; max-height:30vh; overflow:auto }
button { font:inherit; border-radius:12px; border:1px solid var(--edge); background:var(--card);
  color:var(--text); padding:12px 14px; min-height:46px; cursor:pointer }
button:active { transform:scale(.985) }
.go { background:#2f7d4f; border-color:#2f7d4f; color:#fff; font-weight:600 }
.no { background:#3a2430; border-color:#7d3a4b }
.row { display:flex; gap:8px }
.row button { flex:1 }
footer { position:fixed; left:0; right:0; bottom:0; z-index:9; display:flex; gap:8px; align-items:flex-end;
  padding:8px 10px calc(10px + env(safe-area-inset-bottom)); background:rgba(15,17,22,.97);
  border-top:1px solid var(--edge); backdrop-filter:blur(12px) }
textarea { flex:1; min-height:48px; max-height:38vh; resize:none; font:16px/1.45 inherit; padding:12px;
  border-radius:14px; border:1px solid var(--edge); background:var(--card); color:var(--text) }
textarea:focus { outline:none; border-color:var(--accent) }
#stop { display:flex; align-items:center; justify-content:center; min-width:66px; color:var(--bad) }
#stop[hidden] { display:none }
</style></head><body>
<header><span id="dot"></span><b>teveus</b>
  <button id="sessbtn" type="button" title="Switch session"></button>
  <button id="modebtn" type="button" title="Change permission mode"></button>
  <span class="meta" id="meta"></span></header>
<div id="modes" hidden><div class="sheet" id="modelist"></div></div>
<div id="sessions" hidden><div class="sheet" id="sessionlist"></div></div>
<main id="chat"><div class="hint" id="hint">Nothing here yet. Type a message below.</div></main>
<div id="ask" hidden><h2 id="asktitle"></h2><div id="askwhy" hidden></div>
  <pre id="askbody"></pre>
  <div class="row"><button class="go" data-a="allow">Allow</button>
    <button id="always" data-a="always" hidden></button>
    <button class="no" data-a="deny">Deny</button></div>
  <div id="asknote" hidden></div></div>
<footer><textarea id="msg" placeholder="Message…" rows="1"></textarea>
  <button class="go" id="send" type="button">Send</button>
  <button id="stop" hidden type="button" title="Stop">■</button></footer>
<script>
const t = location.pathname.replace(/^\/p\//, '') || new URLSearchParams(location.search).get('t') || '';
const q = p => p + '?t=' + encodeURIComponent(t);
const post = (p, b) => fetch(q(p), {method:'POST', headers:{'Content-Type':'application/json'},
  body: JSON.stringify(b || {})});
const chat = document.getElementById('chat'), hint = document.getElementById('hint');
const meta = document.getElementById('meta'), dot = document.getElementById('dot');
const ask = document.getElementById('ask'), asktitle = document.getElementById('asktitle');
const askbody = document.getElementById('askbody'), always = document.getElementById('always');
const askwhy = document.getElementById('askwhy'), asknote = document.getElementById('asknote');
const modebtn = document.getElementById('modebtn'), modesEl = document.getElementById('modes');
const modeList = document.getElementById('modelist');
const sessbtn = document.getElementById('sessbtn'), sessEl = document.getElementById('sessions');
const sessList = document.getElementById('sessionlist');
const ta = document.getElementById('msg'), stop = document.getElementById('stop');
let modeset = [], modeNow = '', tabs = [], tabNow = 0;
let nearBottom = true;
let first = true;
addEventListener('scroll', () => {
  nearBottom = innerHeight + scrollY >= document.body.scrollHeight - 120;
}, {passive:true});

function atBottom() { scrollTo({top: document.body.scrollHeight, behavior: first ? 'auto' : 'smooth'}) }

function render(v) {
  const chatAtBottom = nearBottom;
  meta.textContent = [v.where, v.model].filter(Boolean).join('\n');
  dot.className = v.busy ? 'busy' : '';
  stop.hidden = !v.busy;
  modeNow = v.mode || '';
  modebtn.textContent = '● ' + (v.mode || 'mode');
  modeset = v.modes || [];
  tabs = v.sessions || [];
  tabNow = v.cur || 0;
  const cur = tabs[tabNow];
  sessbtn.textContent = '▤ ' + (cur ? cur.title : 'session');
  sessbtn.className = cur && cur.state !== 'idle' ? cur.state : '';
  // Any other window may be waiting for an approval, even while this one is idle.
  const anyApproval = tabs.some(tb => tb.win && tb.state === 'approval');
  if (anyApproval && sessbtn.className === '') sessbtn.className = 'approval';
  const key = JSON.stringify(v.msgs || []);
  if (chat.dataset.key !== key) {
    chat.dataset.key = key;
    const keep = scrollY;
    chat.textContent = '';
    (v.msgs || []).forEach(m => {
      const d = document.createElement('div');
      d.className = 'msg ' + m.role + (m.state ? ' ' + m.state : '');
      d.textContent = (m.role === 'tool' ? (m.state === 'failed' ? '✗ ' : m.state === 'running' ? '… ' : '✓ ') : '') + m.text;
      chat.appendChild(d);
    });
    if ((v.msgs || []).length > 0) hint.remove(); else chat.appendChild(hint);
    if (chatAtBottom) { atBottom(); first = false } else scrollTo({top: keep});
  }
  if (v.perm) {
    ask.hidden = false;
    document.body.classList.add('asking');
    asktitle.textContent = 'Approve ' + v.perm.tool + (v.perm.more ? ' (+' + v.perm.more + ' more)' : '');
    askwhy.textContent = v.perm.reason || '';
    askwhy.hidden = !v.perm.reason;
    askbody.textContent = v.perm.detail || '';
    always.hidden = !v.perm.always;
    always.textContent = v.perm.alwaysLabel || 'Always allow';
    asknote.textContent = v.perm.note || '';
    asknote.hidden = !v.perm.note;
    ask.dataset.id = v.perm.id;
  } else {
    ask.hidden = true;
    document.body.classList.remove('asking');
  }
}

async function poll() {
  try { render(await (await fetch(q('/state'))).json()) }
  catch (e) { meta.textContent = 'disconnected'; dot.className = '' }
}

ask.querySelectorAll('button').forEach(b => b.onclick = async () => {
  ask.querySelectorAll('button').forEach(x => x.disabled = true);
  await post('/perm', {action: b.dataset.a, id: ask.dataset.id});
  poll();
});

async function send() {
  const text = ta.value.trim(); if (!text) return;
  ta.value = ''; ta.style.height = 'auto';
  await post('/prompt', {text});
  nearBottom = true; poll();
}
modebtn.onclick = () => {
  modeList.textContent = '';
  modeset.forEach(md => {
    const b = document.createElement('button');
    b.className = md.now ? 'now' : '';
    const lbl = document.createElement('span'); lbl.className = 'lbl'; lbl.textContent = md.label;
    if (md.now) { const t = document.createElement('span'); t.className = 'tick'; t.textContent = '✓'; lbl.appendChild(t) }
    const help = document.createElement('div'); help.className = 'help'; help.textContent = md.help;
    b.appendChild(lbl); b.appendChild(help);
    b.onclick = async () => { modesEl.hidden = true; await post('/mode', {mode: md.id}); poll() };
    modeList.appendChild(b);
  });
  modesEl.hidden = false;
};
modesEl.onclick = e => { if (e.target === modesEl) modesEl.hidden = true };

sessbtn.onclick = () => {
  sessList.textContent = '';
  tabs.forEach(tb => {
    const b = document.createElement('button');
    b.className = tb.now ? 'now' : '';
    const lbl = document.createElement('span'); lbl.className = 'lbl'; lbl.textContent = tb.title;
    if (tb.now) { const k = document.createElement('span'); k.className = 'tick'; k.textContent = '✓'; lbl.appendChild(k) }
    const state = document.createElement('div'); state.className = 'help';
    state.textContent = (tb.state === 'approval' ? 'waiting for your approval'
      : tb.state === 'working' ? 'working…' : 'idle') + (tb.win ? ' · another window' : '');
    b.appendChild(lbl); b.appendChild(state);
    if (tb.win) {
      // Another window: its own page is the only way to act on it.
      b.onclick = () => { location.href = tb.url };
    } else {
      b.onclick = async () => { sessEl.hidden = true; await post('/session', {id: tb.id}); nearBottom = true; poll() };
    }
    sessList.appendChild(b);
  });
  sessEl.hidden = false;
};
sessEl.onclick = e => { if (e.target === sessEl) sessEl.hidden = true };
document.getElementById('send').onclick = send;
ta.addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
});
ta.addEventListener('input', () => {
  ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight, innerHeight * .38) + 'px';
});
stop.onclick = () => post('/stop').then(poll);
addEventListener('resize', poll);
poll(); setInterval(poll, 1200);
</script></body></html>
`
