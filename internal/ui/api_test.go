package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

// mockProvider is an OpenAI-compatible server: first call asks to Write a
// file, second replies with text.
func mockProvider(t *testing.T) *httptest.Server {
	var mu sync.Mutex
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			io.WriteString(w, `{"data":[{"id":"coder-large"},{"id":"coder-small"}]}`)
			return
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(s string) { fmt.Fprintf(w, "data: %s\n\n", s); w.(http.Flusher).Flush() }
		if n == 1 {
			args, _ := json.Marshal(`{"file_path":"hello.txt","content":"hi from api\n"}`)
			send(`{"choices":[{"delta":{"content":"Creating it."}}]}`)
			send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"Write","arguments":` + string(args) + `}}]}}]}`)
			send(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1200,"completion_tokens":40}}`)
		} else {
			send(`{"choices":[{"delta":{"content":"Created **hello.txt**."}}]}`)
			send(`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1300,"completion_tokens":8}}`)
		}
		send("[DONE]")
	}))
}

func TestAPIEngineLoginAndTurn(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	for _, v := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(v, "")
	}
	srv := mockProvider(t)
	defer srv.Close()
	dir := t.TempDir()

	m := New(Config{Claude: claude.Options{Cwd: dir}, Dark: true, Engine: "api"})
	msgs := make(chan tea.Msg, 1024)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					run(c)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	step := func(msg tea.Msg) { _, c := m.Update(msg); run(c) }
	typ := func(s string) {
		for _, r := range s {
			step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	key := func(k tea.KeyType) { step(tea.KeyMsg{Type: k}) }
	pump := func(d time.Duration, until func() bool) {
		deadline := time.After(d)
		for !until() {
			select {
			case msg := <-msgs:
				step(msg)
			case <-deadline:
				t.Fatalf("timeout; view:\n%s", plain(m.View()))
			}
		}
	}
	snap := func(name string) { t.Log(name + ":\n" + plain(m.View())) }

	run(m.Init())
	step(tea.WindowSizeMsg{Width: 110, Height: 32})
	pump(10*time.Second, func() bool { return m.sessionID != "" })
	snap("no provider yet")

	typ("/login")
	key(tea.KeyEnter)
	snap("login picker")
	typ("custom")
	key(tea.KeyEnter)
	typ(srv.URL)
	key(tea.KeyEnter)
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-test\n"), Paste: true})
	snap("key field (masked)")
	key(tea.KeyEnter)
	pump(10*time.Second, func() bool { return len(m.models) == 2 && m.pop.mode == popPicker })
	snap("model picker after login")
	key(tea.KeyEnter) // coder-large
	if m.model != "custom/coder-large" {
		t.Fatalf("model = %q", m.model)
	}
	auth, _ := os.ReadFile(filepath.Join(os.Getenv("TEVEUS_CONFIG"), "auth.json"))
	info, _ := os.Stat(filepath.Join(os.Getenv("TEVEUS_CONFIG"), "auth.json"))
	t.Logf("auth.json mode=%v has key=%v", info.Mode().Perm(), strings.Contains(string(auth), "sk-test"))

	typ("make hello.txt")
	key(tea.KeyEnter)
	pump(10*time.Second, func() bool {
		if len(m.perms) > 0 {
			snap("approval")
			typ("y")
		}
		return !m.busy && len(m.blocks) > 3
	})
	b, _ := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if string(b) != "hi from api\n" {
		t.Fatalf("hello.txt = %q", b)
	}
	snap("after turn")
	m.client.Close()
}
