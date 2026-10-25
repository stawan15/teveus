package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
	if !strings.Contains(url, "?t=") {
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
	resp, err := http.Get(p.baseURL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", resp.StatusCode)
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
}

func TestPhoneShowsTranscriptAndTakesAPrompt(t *testing.T) {
	m, p, msgs := phoneTestModel(t)
	m.add(&block{kind: kindUser, text: "list the tests"})
	m.add(&block{kind: kindAssistant, text: "There are three."})
	m.refresh()

	var v phoneView
	phoneGet(t, p, "/state", &v)
	if !strings.Contains(v.Transcript, "list the tests") || !strings.Contains(v.Transcript, "There are three.") {
		t.Fatalf("transcript = %q", v.Transcript)
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
