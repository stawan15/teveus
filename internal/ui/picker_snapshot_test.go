package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

func TestPickerSnapshot(t *testing.T) {
	fixture := os.Getenv("OR_MODELS")
	if fixture == "" {
		t.Skip()
	}
	data, _ := os.ReadFile(fixture)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write(data)
			return
		}
		w.WriteHeader(402)
		io.WriteString(w, `{"error":{"message":"This request requires more credits, or fewer max_tokens. You requested up to 16000 tokens, but can only afford 212."}}`)
	}))
	defer srv.Close()
	cfg := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", cfg)
	agent.NewStore(cfg).Save("openrouter", agent.Credential{Key: "k", BaseURL: srv.URL})

	m := New(Config{Claude: claude.Options{Cwd: t.TempDir()}, Dark: true, Engine: "api"})
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
			if r == ' ' {
				step(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			} else {
				step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
		}
	}
	pump := func(until func() bool) {
		deadline := time.After(10 * time.Second)
		for !until() {
			select {
			case msg := <-msgs:
				step(msg)
			case <-deadline:
				t.Fatal("timeout")
			}
		}
	}
	run(m.Init())
	step(tea.WindowSizeMsg{Width: 150, Height: 44})
	pump(func() bool { return m.pop.mode == popPicker })
	t.Log("picker:\n" + plain(m.View()))
	typ("claude")
	t.Log("filter claude:\n" + plain(m.View()))
	step(tea.KeyMsg{Type: tea.KeyCtrlU})
	for range 6 {
		step(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typ("free")
	t.Log("filter free:\n" + plain(m.View()))
	step(tea.KeyMsg{Type: tea.KeyEnter})
	typ("hi")
	step(tea.KeyMsg{Type: tea.KeyEnter})
	pump(func() bool { return !m.busy && len(m.blocks) >= 2 })
	t.Log("credit error:\n" + plain(m.View()))
	m.client.Close()
}
