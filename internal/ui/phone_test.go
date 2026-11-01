package ui

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

// phoneTestModel is a Model with a phone server on a free port, and the
// messages it sends collected instead of going to a program.
func phoneTestModel(t *testing.T) (*Model, *phoneServer, chan tea.Msg) {
	t.Helper()
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	msgs := make(chan tea.Msg, 8)
	url, err := m.startPhone("127.0.0.1:0", func(msg tea.Msg) { msgs <- msg })
	if err != nil {
		t.Fatalf("startPhone: %v", err)
	}
	t.Cleanup(m.stopPhone)
	if !strings.Contains(url, "/p/") {
		t.Fatalf("url = %q, want a token", url)
	}
	return m, m.phone, msgs
}

func phoneGet(t *testing.T, p *phoneServer, path string, out any) int {
	t.Helper()
	resp, err := http.Get(p.baseURL() + path + "?t=" + p.token)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

func phonePost(t *testing.T, p *phoneServer, path, body string) int {
	t.Helper()
	resp, err := http.Post(p.baseURL()+path+"?t="+p.token, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestPhoneMovesOnWhenThePortIsTaken(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	// An older teveus may still hold the default port.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	_, freePort, _ := net.SplitHostPort(taken.Addr().String())

	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	url, err := m.startPhone("127.0.0.1:"+freePort, func(tea.Msg) {})
	if err != nil {
		t.Fatalf("startPhone on a taken port: %v", err)
	}
	t.Cleanup(m.stopPhone)
	got := strings.TrimPrefix(strings.Split(url, "/?t=")[0], "http://")
	_, want, _ := net.SplitHostPort(got)
	if want == freePort {
		t.Fatalf("kept the taken port %s", freePort)
	}
}

func TestPhonePageOffUntilTurnedOn(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	var sent []tea.Msg
	m.cfg.Send = func(msg tea.Msg) { sent = append(sent, msg) }

	if m.phoneOn() || m.settings.PhonePage {
		t.Fatal("the phone page is on for a new install")
	}
	m.startPhoneServer()
	if m.phoneOn() {
		t.Fatal("the page started without being asked for")
	}

	if cmd := m.togglePhone(); cmd != nil {
		t.Fatal("turning it on shouldn't block the UI")
	}
	t.Cleanup(m.stopPhone)
	if !m.phoneOn() || !LoadSettings().PhonePage {
		t.Fatal("turning it on didn't start the page and save the choice")
	}

	m.togglePhone()
	if m.phoneOn() || LoadSettings().PhonePage {
		t.Fatal("turning it off didn't stop the page and save the choice")
	}
}

func TestPhonePageNeedsTheToken(t *testing.T) {
	_, p, _ := phoneTestModel(t)
	// The page itself only exists once the cookie has been handed out, or the
	// link's token is presented.
	resp, err := http.Get(p.baseURL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bare root = %d, want 401", resp.StatusCode)
	}
	resp, err = http.Get(p.baseURL() + "/p/wrong")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong path token = %d, want 401", resp.StatusCode)
	}
	resp, err = http.Get(p.baseURL() + "/state?t=wrong")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", resp.StatusCode)
	}
	if code := phoneGet(t, p, "/state", nil); code != http.StatusOK {
		t.Fatalf("good token = %d", code)
	}
	// The link's own form works without a query string, and trades the token in
	// the URL for a cookie the page never sees.
	link := p.baseURL() + "/p/" + p.token
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	// Look at the first hop on its own: it is the one that sets the cookie.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	first, err := noRedirect.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("opening the link = %d, want a redirect to /", first.StatusCode)
	}
	var attrs []string
	for _, sc := range first.Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, cookieName+"=") {
			attrs = append(attrs, strings.ToLower(sc))
		}
	}
	if len(attrs) == 0 {
		t.Fatal("opening the link didn't hand out a Set-Cookie")
	}
	for _, want := range []string{"httponly", "samesite=strict", "path=/"} {
		if !strings.Contains(attrs[0], want) {
			t.Fatalf("cookie %q is missing %q", attrs[0], want)
		}
	}
	resp, err = client.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("opening the link = %d", resp.StatusCode)
	}
	if resp.Request.URL.Path != "/" {
		t.Fatalf("opening the link left the token in the URL: %s", resp.Request.URL)
	}
	u, _ := url.Parse(p.baseURL())
	var found bool
	for _, c := range jar.Cookies(u) {
		if c.Name == cookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("the link didn't hand out the token cookie")
	}
	if strings.Contains(string(body), p.token) {
		t.Fatal("the page carries the token in its body")
	}
	// The cookie alone is enough for the API.
	req, _ := http.NewRequest("GET", p.baseURL()+"/state", nil)
	cres, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cres.Body.Close()
	if cres.StatusCode != http.StatusOK {
		t.Fatalf("cookie-only /state = %d", cres.StatusCode)
	}
}

func TestPhoneSecurityHeaders(t *testing.T) {
	_, p, _ := phoneTestModel(t)
	req, _ := http.NewRequest("GET", p.baseURL()+"/p/"+p.token+"", nil)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for h, want := range map[string]string{
		"Referrer-Policy":            "no-referrer",
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "nonce-"+p.nonce) || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP = %q", csp)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if p.nonce == "" || strings.Contains(string(phonePageHTML(p.nonce)), "{{NONCE}}") {
		t.Fatal("the page kept its nonce placeholder")
	}
}

// The phone can tighten permissions but never loosen them: a link that leaks
// must not be able to turn the agent loose on the machine.
func TestPhoneModeOnlyGoesStricter(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.mode = "default"
	m.refresh()
	if code := phonePost(t, p, "/mode", `{"mode":"auto"}`); code != http.StatusForbidden {
		t.Fatalf("auto = %d, want 403", code)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("a looser mode was sent: %#v", msg)
	default:
	}
	if code := phonePost(t, p, "/mode", `{"mode":"plan"}`); code != http.StatusNoContent {
		t.Fatalf("plan = %d, want 204", code)
	}
	select {
	case msg := <-msgs:
		pm, ok := msg.(phoneMsg)
		if !ok || pm.mode != "plan" {
			t.Fatalf("message = %#v", msg)
		}
	default:
		t.Fatal("the stricter mode never reached the UI")
	}
}

func TestPhoneCantGrantPermanentApproval(t *testing.T) {
	_, p, msgs := phoneTestModel(t)
	p.mu.Lock()
	p.view.Perm = &phonePerm{ID: "req-1", Tool: "Bash", Detail: "$ rm -rf build"}
	p.mu.Unlock()
	if code := phonePost(t, p, "/perm", `{"action":"always","id":"req-1"}`); code != http.StatusForbidden {
		t.Fatalf("always = %d, want 403", code)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("a permanent approval was sent: %#v", msg)
	default:
	}
	if code := phonePost(t, p, "/perm", `{"action":"allow","id":"req-1"}`); code != http.StatusNoContent {
		t.Fatalf("allow = %d, want 204", code)
	}
}

func TestPhoneLinkStaysTheSameBetweenRuns(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	first := New(Config{Dark: true})
	u1, err := first.startPhone("127.0.0.1:0", func(tea.Msg) {})
	if err != nil {
		t.Fatal(err)
	}
	first.stopPhone()

	second := New(Config{Dark: true})
	u2, err := second.startPhone("127.0.0.1:0", func(tea.Msg) {})
	if err != nil {
		t.Fatal(err)
	}
	second.stopPhone()

	tok := func(u string) string { return u[strings.LastIndex(u, "/")+1:] }
	if tok(u1) == "" || tok(u1) != tok(u2) {
		t.Fatalf("tokens differ between runs: %q then %q", u1, u2)
	}
}

func TestPhoneShowsTranscriptAndTakesAPrompt(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.add(&block{kind: kindUser, text: "list the tests"})
	m.add(&block{kind: kindAssistant, text: "There are three."})
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if len(v.Msgs) != 2 {
		t.Fatalf("messages = %+v", v.Msgs)
	}
	if v.Msgs[0].Role != "you" || v.Msgs[0].Text != "list the tests" {
		t.Fatalf("first message = %+v", v.Msgs[0])
	}
	if v.Msgs[1].Role != "ai" || !strings.Contains(v.Msgs[1].Text, "There are three.") {
		t.Fatalf("second message = %+v", v.Msgs[1])
	}
	if v.Perm != nil {
		t.Fatal("nothing is waiting for an answer, yet the page shows a prompt")
	}

	if code := phonePost(t, p, "/prompt", `{"text":"  run go test  "}`); code != http.StatusNoContent {
		t.Fatalf("prompt = %d", code)
	}
	select {
	case msg := <-msgs:
		pm, ok := msg.(phoneMsg)
		if !ok || pm.text != "run go test" {
			t.Fatalf("message = %#v", msg)
		}
	default:
		t.Fatal("the prompt never reached the UI")
	}

	phonePost(t, p, "/prompt", `{"text":"   "}`)
	select {
	case msg := <-msgs:
		t.Fatalf("a blank prompt was sent: %#v", msg)
	default:
	}
}

func TestPhoneApprovesTheQuestionOnScreen(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.client = &tabBackend{ch: make(chan claude.Event)}
	req := &claude.PermissionRequest{RequestID: "req-1", ToolName: "Bash",
		Input: json.RawMessage(`{"command":"go test ./..."}`)}
	m.perms = append(m.perms, req)
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if v.Perm == nil || v.Perm.ID != "req-1" || v.Perm.Tool != "Bash" {
		t.Fatalf("perm = %+v", v.Perm)
	}
	if !strings.Contains(v.Perm.Detail, "go test ./...") {
		t.Fatalf("detail = %q", v.Perm.Detail)
	}

	// A stale answer (the terminal already said yes) is refused, not applied.
	if code := phonePost(t, p, "/perm", `{"action":"allow","id":"req-0"}`); code != http.StatusConflict {
		t.Fatalf("stale answer = %d, want 409", code)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("a stale answer was sent on: %#v", msg)
	default:
	}

	if code := phonePost(t, p, "/perm", `{"action":"deny","id":"req-1"}`); code != http.StatusNoContent {
		t.Fatalf("deny = %d", code)
	}
	msg := (<-msgs).(phoneMsg)
	if msg.perm != "deny" || msg.request != "req-1" {
		t.Fatalf("message = %#v", msg)
	}

	// Applying it clears the prompt, so the page stops asking.
	m.handlePhone(msg)
	if len(m.perms) != 0 {
		t.Fatalf("%d questions still waiting", len(m.perms))
	}
	phoneGet(t, p, "/state", &v)
	if v.Perm != nil {
		t.Fatalf("the page still asks: %+v", v.Perm)
	}
}

func TestPhoneSwitchesPermissionMode(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.mode = "default"
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if v.Mode != "ask first" {
		t.Fatalf("mode = %q", v.Mode)
	}
	if len(v.Modes) != len(modes) {
		t.Fatalf("%d modes, want %d", len(v.Modes), len(modes))
	}
	for _, md := range v.Modes {
		if md.ID == "plan" && (!md.Now == false || md.Label != "plan only" || md.Help == "") {
			t.Fatalf("plan row = %+v", md)
		}
	}

	if code := phonePost(t, p, "/mode", `{"mode":"nonsense"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown mode = %d, want 400", code)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("an unknown mode was sent on: %#v", msg)
	default:
	}

	if code := phonePost(t, p, "/mode", `{"mode":"plan"}`); code != http.StatusNoContent {
		t.Fatalf("plan = %d", code)
	}
	msg := (<-msgs).(phoneMsg)
	if msg.mode != "plan" {
		t.Fatalf("message = %#v", msg)
	}
	m.handlePhone(msg)
	if m.mode != "plan" {
		t.Fatalf("mode = %q after the phone picked plan", m.mode)
	}
	phoneGet(t, p, "/state", &v)
	if v.Mode != "plan only" {
		t.Fatalf("page still shows %q", v.Mode)
	}
}

func TestPhoneApprovalShowsWhatAndWhy(t *testing.T) {
	m, p, _ := phoneTestModel(t)
	m.perms = append(m.perms, &claude.PermissionRequest{RequestID: "w1", ToolName: "Write",
		Description: "outside the project, so it always asks",
		Input:       json.RawMessage(`{"file_path":"/tmp/notes.md","content":"hello"}`)})
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if v.Perm == nil {
		t.Fatal("no prompt on the page")
	}
	if v.Perm.Reason != "outside the project, so it always asks" {
		t.Fatalf("reason = %q", v.Perm.Reason)
	}
	if !strings.Contains(v.Perm.Detail, "/tmp/notes.md") || !strings.Contains(v.Perm.Detail, "hello") {
		t.Fatalf("detail = %q", v.Perm.Detail)
	}
	if v.Perm.Note == "" {
		t.Fatal("the page doesn't say what denying does")
	}
}

func TestPhoneSwitchesSession(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	// The session on screen keeps its blocks in the model; the others are parked.
	m.blocks = []*block{{kind: kindUser, text: "first conversation"}}
	m.sessions = append(m.sessions, &session{follow: true, blocks: []*block{{kind: kindUser, text: "second one"}}, busy: true})
	m.cur = 0
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if len(v.Sessions) != 2 || v.Cur != 0 {
		t.Fatalf("sessions = %+v cur=%d", v.Sessions, v.Cur)
	}
	if v.Sessions[0].Title != "first conversation" || !v.Sessions[0].Now || v.Sessions[0].State != "idle" {
		t.Fatalf("first tab = %+v", v.Sessions[0])
	}
	if v.Sessions[1].State != "working" || v.Sessions[1].Now {
		t.Fatalf("second tab = %+v", v.Sessions[1])
	}

	if code := phonePost(t, p, "/session", `{"id":5}`); code != http.StatusNotFound {
		t.Fatalf("missing session = %d, want 404", code)
	}
	if code := phonePost(t, p, "/session", `{}`); code != http.StatusBadRequest {
		t.Fatalf("no id = %d, want 400", code)
	}

	if code := phonePost(t, p, "/session", `{"id":1}`); code != http.StatusNoContent {
		t.Fatalf("switch = %d", code)
	}
	msg := (<-msgs).(phoneMsg)
	if msg.session == nil || *msg.session != 1 {
		t.Fatalf("message = %#v", msg)
	}
	m.handlePhone(msg)
	if m.cur != 1 {
		t.Fatalf("on session %d after the phone switched", m.cur)
	}
	phoneGet(t, p, "/state", &v)
	if v.Cur != 1 || !v.Sessions[1].Now {
		t.Fatalf("page still on %d", v.Cur)
	}
}

func TestPhoneListsOtherWindows(t *testing.T) {
	// This test plays one window; a real server stands in for another, and a
	// handle file tells this one where that is.
	m, p, _ := phoneTestModel(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "other-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"busy":true,"tab":"deploy to prod","where":"~/elsewhere"}`)
	}))
	defer other.Close()

	otherAddr := strings.TrimPrefix(other.URL, "http://")
	dir := handleDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h, _ := json.Marshal(phoneHandle{PID: os.Getppid(), Addr: otherAddr, Token: "other-token",
		Title: "deploy to prod", Folder: "~/elsewhere", At: time.Now().Unix()})
	path := filepath.Join(dir, strconv.Itoa(os.Getppid())+".json")
	if err := os.WriteFile(path, h, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	p.pollWindows()
	var v phoneView
	p.snapshot(m)
	phoneGet(t, p, "/state", &v)

	var found *phoneTab
	for i := range v.Sessions {
		if v.Sessions[i].Win == otherAddr {
			found = &v.Sessions[i]
		}
	}
	if found == nil {
		t.Fatalf("the other window isn't listed: %+v", v.Sessions)
	}
	if found.Title != "deploy to prod · elsewhere" || found.State != "working" {
		t.Fatalf("other window = %+v", found)
	}
	if found.Now {
		t.Fatal("another window's session is marked as the one on screen")
	}

	// The page may open that window, but it never learns that window's token:
	// every window of cette install shares one, so a page holding it could act
	// as any of them.
	var raw map[string]any
	phoneGet(t, p, "/state", &raw)
	if b, _ := json.Marshal(raw); strings.Contains(string(b), "other-token") || strings.Contains(string(b), "/p/") {
		t.Fatalf("/state leaks another window's link: %s", b)
	}
	// /open builds the link on the server instead.
	req, _ := http.NewRequest("POST", p.baseURL()+"/open?t="+p.token, strings.NewReader(`{"win":"`+otherAddr+`"}`))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "http://"+otherAddr+"/p/other-token" {
		t.Fatalf("open = %d %q", resp.StatusCode, loc)
	}
	if code := phonePost(t, p, "/open", `{"win":"10.0.0.9:9999"}`); code != http.StatusNotFound {
		t.Fatalf("unknown window = %d, want 404", code)
	}

	// A handle whose process is gone is dropped, not listed.
	deadPath := filepath.Join(dir, "424243.json")
	dead, _ := json.Marshal(phoneHandle{PID: 999999, Addr: otherAddr, Token: "x", At: time.Now().Unix()})
	if err := os.WriteFile(deadPath, dead, 0o600); err != nil {
		t.Fatal(err)
	}
	p.pollWindows()
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Fatal("a handle for a dead process was kept")
	}
}

func TestPhoneListsSlashCommands(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if len(v.Cmds) != len(localCmds) {
		t.Fatalf("%d commands on the page, want %d", len(v.Cmds), len(localCmds))
	}
	byName := map[string]string{}
	for _, c := range v.Cmds {
		byName[c.Name] = c.Desc
	}
	for _, want := range []string{"model", "mode", "clear", "sessions"} {
		if byName[want] == "" {
			t.Fatalf("/%s is missing from the page's commands", want)
		}
	}

	// Running one goes through the same path as typing it in the terminal.
	if code := phonePost(t, p, "/prompt", `{"text":"/mode"}`); code != http.StatusNoContent {
		t.Fatalf("command = %d", code)
	}
	msg := (<-msgs).(phoneMsg)
	if msg.text != "/mode" {
		t.Fatalf("message = %#v", msg)
	}
}

func TestPhoneRejectsABadAction(t *testing.T) {
	m, p, _ := phoneTestModel(t)
	m.perms = append(m.perms, &claude.PermissionRequest{RequestID: "r", ToolName: "Bash"})
	m.refresh()
	if code := phonePost(t, p, "/perm", `{"action":"maybe","id":"r"}`); code != http.StatusBadRequest {
		t.Fatalf("bad action = %d, want 400", code)
	}
	// Answers must be POSTs, so a link can't approve anything.
	resp, err := http.Get(p.baseURL() + "/perm?t=" + p.token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /perm = %d, want 405", resp.StatusCode)
	}
}

func TestPhoneAnswersTheSubWindowTheTerminalOpened(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.engine = "api"
	m.model = "openai/gpt-5.4"

	// /model opens a picker in the terminal. Until now the phone showed nothing
	// and the command did nothing there.
	var picked string
	m.openPicker("Model", []choice{
		{label: "gpt-5.4", desc: "openai", run: func(m *Model) tea.Cmd { picked = "gpt-5.4"; return nil }},
		{label: "gemini-3.5", desc: "google", run: func(m *Model) tea.Cmd { picked = "gemini-3.5"; return nil }},
	}, 0)
	m.refresh() // the idle tick is what makes an open picker visible

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if v.Prompt == nil || v.Prompt.Kind != "pick" {
		t.Fatalf("prompt = %+v", v.Prompt)
	}
	if v.Prompt.Title != "Model" || len(v.Prompt.Opts) != 2 || !v.Prompt.Opts[0].Now {
		t.Fatalf("prompt = %+v", v.Prompt)
	}

	if code := phonePost(t, p, "/popup", `{"index":1}`); code != http.StatusNoContent {
		t.Fatalf("answer = %d", code)
	}
	msg := (<-msgs).(phoneMsg)
	if !msg.popup || msg.index != 1 {
		t.Fatalf("message = %#v", msg)
	}
	m.handlePhone(msg)
	if picked != "gemini-3.5" {
		t.Fatalf("picked %q", picked)
	}
	if m.pop.open() {
		t.Fatal("the picker stayed open after the phone chose")
	}
	// A fresh value: decoding into an old struct keeps fields the JSON omits.
	var after phoneView
	phoneGet(t, p, "/state", &after)
	if after.Prompt != nil {
		t.Fatalf("the page still shows %+v", after.Prompt)
	}
}

func TestPhoneAnswersAnInputBox(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	var got string
	m.pop = popup{mode: popInput, title: "Brave key", masked: true,
		submit: func(m *Model, v string) tea.Cmd { got = v; return nil }}
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if v.Prompt == nil || v.Prompt.Kind != "input" || !v.Prompt.Masked {
		t.Fatalf("prompt = %+v", v.Prompt)
	}
	if code := phonePost(t, p, "/popup", `{"text":"sk-abc"}`); code != http.StatusNoContent {
		t.Fatalf("answer = %d", code)
	}
	m.handlePhone((<-msgs).(phoneMsg))
	if got != "sk-abc" {
		t.Fatalf("submitted %q", got)
	}
}

func TestPhonePopupAnswerNeedsSomethingOpen(t *testing.T) {
	_, p, _ := phoneTestModel(t)
	if code := phonePost(t, p, "/popup", `{"index":0}`); code != http.StatusConflict {
		t.Fatalf("answer with nothing open = %d, want 409", code)
	}
}

func TestPhoneShowsQueuedMessages(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.client = &tabBackend{ch: make(chan claude.Event, 8)}
	m.busy = true
	m.refresh()

	// Sending while it works queues the message instead of cutting the reply.
	phonePost(t, p, "/prompt", `{"text":"also update the docs"}`)
	m.handlePhone((<-msgs).(phoneMsg))
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if len(v.Queue) != 1 || v.Queue[0] != "also update the docs" {
		t.Fatalf("queue on the page = %v", v.Queue)
	}
	if !v.Busy {
		t.Fatal("the page doesn't say it is still working")
	}

	// The turn ends: the queued message goes next, and the page says so.
	m.busy = false
	m.sendQueued()
	m.refresh()
	var after phoneView
	phoneGet(t, p, "/state", &after)
	if len(after.Queue) != 0 {
		t.Fatalf("queue after the turn ended = %v", after.Queue)
	}
}

// injectingBackend is a backend whose loop can take a message mid-turn.
type injectingBackend struct {
	tabBackend
	injected []string
}

func (b *injectingBackend) Inject(text string) error {
	b.injected = append(b.injected, text)
	return nil
}

func TestPhoneMessageJoinsTheRunningTurn(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	b := &injectingBackend{tabBackend: tabBackend{ch: make(chan claude.Event, 8)}}
	m.client = b
	m.busy = true
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if !v.Inline {
		t.Fatal("the page doesn't know a message can join the running turn")
	}

	phonePost(t, p, "/prompt", `{"text":"also the tests"}`)
	m.handlePhone((<-msgs).(phoneMsg))

	// Straight into the turn that is running, not the queue that waits for it.
	if len(b.injected) != 1 || b.injected[0] != "also the tests" {
		t.Fatalf("injected = %v", b.injected)
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue = %v, want the message to be in the turn", m.queue)
	}
}

func TestPhoneMessageQueuesWhenTheBackendCannotInject(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.client = &tabBackend{ch: make(chan claude.Event, 8)} // no Inject
	m.busy = true
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if v.Inline {
		t.Fatal("the page claims it can inject, but the backend can't")
	}
	phonePost(t, p, "/prompt", `{"text":"later please"}`)
	m.handlePhone((<-msgs).(phoneMsg))
	if len(m.queue) != 1 || m.queue[0] != "later please" {
		t.Fatalf("queue = %v", m.queue)
	}
}

func TestPhoneSendsRepliesAsMarkdownForThePage(t *testing.T) {
	// The page renders markdown into elements; it needs the source intact, not
	// flattened to plain text on the way out.
	m, p, _ := phoneTestModel(t)
	m.blocks = []*block{
		{kind: kindAssistant, text: "## Result\n\nUse `go test` and **bold**.\n\n- one\n- two"},
		{kind: kindTool, name: "Bash", input: map[string]any{"command": "ls"}},
	}
	p.update(m)

	if len(p.view.Msgs) < 2 {
		t.Fatalf("messages = %+v", p.view.Msgs)
	}
	ai := p.view.Msgs[0]
	if ai.Role != "ai" {
		t.Fatalf("first message = %+v", ai)
	}
	for _, want := range []string{"## Result", "`go test`", "**bold**", "- one"} {
		if !strings.Contains(ai.Text, want) {
			t.Fatalf("markdown lost on the way to the page: %q missing from %q", want, ai.Text)
		}
	}
	// Tools are not markdown: their text keeps the state marker instead.
	if tool := p.view.Msgs[1]; tool.Role != "tool" || strings.Contains(tool.Text, "##") {
		t.Fatalf("tool message = %+v", tool)
	}
}

// Trusting a folder and agreeing to run Claude Code are answered in the
// terminal: a leaked link must not be able to widen what the app may touch.
func TestPhoneCantAnswerLocalQuestions(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.askTrust()
	m.refresh()
	phoneGet(t, p, "/state", nil)
	p.mu.Lock()
	prompt := p.view.Prompt
	p.mu.Unlock()
	if prompt == nil || !prompt.Local || prompt.Kind != "local" {
		t.Fatalf("the page was shown an answerable trust prompt: %+v", prompt)
	}
	if code := phonePost(t, p, "/popup", `{"index":0}`); code != http.StatusConflict {
		t.Fatalf("answering the trust prompt = %d, want 409", code)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("the phone answered a local question: %#v", msg)
	default:
	}
}

// A key being typed into a masked field never reaches the page.
func TestPhoneMaskedInputValueStaysLocal(t *testing.T) {
	m, p, _ := phoneTestModel(t)
	m.pop = popup{mode: popInput, title: "API key", masked: true, query: "sk-abcdefghijklmnop"}
	m.refresh()
	phoneGet(t, p, "/state", nil)
	p.mu.Lock()
	ask := p.view.Prompt
	p.mu.Unlock()
	if ask == nil || !ask.Masked {
		t.Fatalf("prompt = %+v", ask)
	}
	if ask.Query != "" {
		t.Fatalf("the masked value reached the page: %q", ask.Query)
	}
}
