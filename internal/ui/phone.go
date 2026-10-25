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
	popup     bool   // an answer to the open picker, slider or input
	index     int    // which row it picked; -1 for a text answer
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
	Cmds     []phoneCmd  `json:"cmds"`
	Queue    []string    `json:"queue"`            // sent while working and waiting for the turn to end
	Inline   bool        `json:"inline"`           // a message said now joins the running turn
	Prompt   *phoneAsk   `json:"prompt,omitempty"` // a picker or slider the terminal is showing
}

// phoneAsk is a picker, slider or text field the app has open, so the phone
// shows the same sub-window the terminal does: /model, /effort, /theme and the
// rest would otherwise do nothing on a phone.
type phoneAsk struct {
	Kind   string     `json:"kind"` // "pick", "slider" or "input"
	Title  string     `json:"title"`
	Hint   string     `json:"hint,omitempty"`
	Opts   []phoneOpt `json:"opts,omitempty"`
	Sel    int        `json:"sel"`
	Masked bool       `json:"masked,omitempty"`
	Query  string     `json:"query,omitempty"`
	Ends   [2]string  `json:"ends,omitempty"` // slider: what the low and high ends mean
}

// phoneOpt is one row of a picker or one level of a slider.
type phoneOpt struct {
	Label string `json:"label"`
	Desc  string `json:"desc,omitempty"`
	Key   string `json:"key,omitempty"`
	Now   bool   `json:"now,omitempty"`
	Held  bool   `json:"held,omitempty"` // can't be picked for a moment
}

// phoneCmd is one slash command the page can run, so the phone gets the same
// shortcuts as the terminal without their names written out twice.
type phoneCmd struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
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
	mux.HandleFunc("/popup", p.popup)
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
	for _, c := range localCmds {
		v.Cmds = append(v.Cmds, phoneCmd{Name: c.name, Desc: c.desc})
	}
	v.Prompt = phonePopup(&m.pop)
	v.Queue = append([]string(nil), m.queue...)
	_, v.Inline = m.client.(claude.Injector)
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

// phonePopup turns the sub-window the terminal has open into something the
// phone can draw and answer. The slash and @ lists belong to the composer the
// page already draws, so those stay in the terminal.
func phonePopup(p *popup) *phoneAsk {
	switch p.mode {
	case popPicker, popPalette, popReview, popSlider, popInput:
	default:
		return nil
	}
	a := &phoneAsk{Kind: "pick", Title: termSafe(p.title), Hint: termSafe(p.hint), Sel: p.sel}
	switch p.mode {
	case popSlider:
		a.Kind, a.Ends = "slider", p.ends
	case popInput:
		a.Kind, a.Masked, a.Query = "input", p.masked, p.query
	}
	for i, c := range p.list {
		a.Opts = append(a.Opts, phoneOpt{Label: termSafe(c.label), Desc: termSafe(c.desc),
			Key: termSafe(c.key), Now: i == p.sel, Held: p.held(c)})
	}
	return a
}

// answerPhonePopup applies what the phone chose in that sub-window, down the
// same path the keyboard takes.
func (m *Model) answerPhonePopup(index int, text string) tea.Cmd {
	p := &m.pop
	switch p.mode {
	case popInput:
		submit := p.submit
		m.closePopup(false)
		if submit != nil {
			return submit(m, text)
		}
	case popSlider, popPicker, popPalette, popReview:
		if index >= 0 && index < len(p.list) {
			p.sel = index
		}
		c, ok := p.current()
		if !ok || p.held(c) {
			return nil
		}
		run := c.run
		m.closePopup(false)
		if run != nil {
			return run(m)
		}
	}
	m.layout()
	m.refresh()
	return nil
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

func (p *phoneServer) popup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index *int   `json:"index"` // a pick or slider level
		Text  string `json:"text"`  // an input box's value
	}
	if !p.decode(w, r, &body) {
		return
	}
	p.mu.Lock()
	open := p.view.Prompt != nil
	p.mu.Unlock()
	if !open {
		http.Error(w, "nothing is open", http.StatusConflict)
		return
	}
	idx := -1
	if body.Index != nil {
		idx = *body.Index
	}
	p.send(phoneMsg{popup: true, index: idx, text: body.Text})
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
