package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func TestEventMsgBatchingCoalescesDeltas(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})

	ch := make(chan claude.Event, 40)
	for i := 0; i < 30; i++ {
		ch <- claude.TextDelta{Text: fmt.Sprintf("w%d ", i)}
	}
	first := <-ch
	_, cmd := m.Update(eventMsg{gen: m.gen, ev: first, ch: ch})
	if cmd == nil {
		t.Fatal("expected listen cmd to continue reading open channel")
	}
	if len(ch) != 0 {
		t.Fatalf("expected buffered deltas to be drained, %d left", len(ch))
	}
	if m.stream == nil {
		t.Fatal("expected active stream block")
	}
	for i := 0; i < 30; i++ {
		want := fmt.Sprintf("w%d ", i)
		if !strings.Contains(m.stream.text, want) {
			t.Fatalf("missing token %q in %q", want, m.stream.text)
		}
	}
}

func TestEventMsgBatchingHandlesClosedChannel(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})

	ch := make(chan claude.Event, 5)
	ch <- claude.TextDelta{Text: "hello "}
	ch <- claude.TextDelta{Text: "world"}
	close(ch)

	first := <-ch
	_, cmd := m.Update(eventMsg{gen: m.gen, ev: first, ch: ch})
	if cmd != nil {
		t.Fatal("expected nil cmd after draining closed channel")
	}
	if m.stream == nil || m.stream.text != "hello world" {
		t.Fatalf("got stream=%v", m.stream)
	}
}

func TestTickOnlyInvalidatesRunningTools(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.busy = true

	done := &block{kind: kindTool, id: "done", name: "Read", state: toolDone}
	running := &block{kind: kindTool, id: "run", name: "Bash", state: toolRunning, started: time.Now()}
	m.tools["done"] = done
	m.tools["run"] = running
	m.add(done)
	m.add(running)
	m.refresh()

	done.dirty = false
	running.dirty = false
	m.Update(tickMsg{})
	if done.dirty {
		t.Fatal("finished tool should not be invalidated by tick")
	}
	if running.dirty {
		// tickMsg calls refresh() when a running tool was invalidated, which re-renders it and clears dirty.
		t.Fatal("running tool should have been refreshed by tick")
	}

	// Now mark running tool done: tickMsg should not touch any block.
	running.state = toolDone
	done.dirty, running.dirty = false, false
	m.Update(tickMsg{})
	if done.dirty || running.dirty {
		t.Fatal("no tools running: tick should not invalidate any tool block")
	}
}

func TestHeaderCacheInvalidatesOnChange(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.model = "claude-sonnet-4-5"
	h1 := m.header()
	h2 := m.header()
	if h1 != h2 {
		t.Fatal("cached header mismatch")
	}
	m.model = "claude-opus-4-5"
	h3 := m.header()
	if h3 == h1 || !strings.Contains(h3, "opus-4-5") {
		t.Fatalf("header did not update on model change: %q", h3)
	}
}

func TestRowCacheDoesNotStallPastLimit(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < 1200; i++ {
		_ = m.fitRow(fmt.Sprintf("unique line %d", i))
	}
	if len(m.rowCache) >= 1024 {
		t.Fatalf("expected rowCache to clear at limit, got len=%d", len(m.rowCache))
	}
	// Newly added line should be cached.
	const probe = "fresh visible line"
	_ = m.fitRow(probe)
	if _, ok := m.rowCache[probe]; !ok {
		t.Fatal("expected fresh line to be cached after rollover")
	}
}

func TestTokenSaversPreserved(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true, Engine: "claude"})
	m.settings.Concise = true
	m.settings.LeanTools = true
	m.settings.MinimalCode = "strict"

	opts := m.applySavers(claude.Options{})
	if !strings.Contains(opts.AppendPrompt, "concise") {
		t.Fatalf("expected concise prompt, got %q", opts.AppendPrompt)
	}
	if len(opts.DisallowTools) == 0 {
		t.Fatal("expected leanDisallowed tools on claude engine")
	}
	if lbl := m.saverLabel(); !strings.Contains(lbl, "concise") || !strings.Contains(lbl, "lean") || !strings.Contains(lbl, "minimal strict") {
		t.Fatalf("unexpected saverLabel: %q", lbl)
	}

	// -full disables all savers.
	m.cfg.Full = true
	fullOpts := m.applySavers(claude.Options{})
	if fullOpts.AppendPrompt != "" || len(fullOpts.DisallowTools) != 0 {
		t.Fatalf("-full should disable savers, got %+v", fullOpts)
	}

	// Direct API engine also displays "lean" in saverLabel.
	mAPI := New(Config{Dark: true, Engine: "api"})
	mAPI.settings.LeanTools = true
	if lbl := mAPI.saverLabel(); !strings.Contains(lbl, "lean") {
		t.Fatalf("expected lean in API saverLabel, got %q", lbl)
	}
}

func TestToolProgressAndLiveTimerRendered(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	tb := &block{
		kind: kindTool, id: "b1", name: "Bash", state: toolRunning,
		input:   map[string]any{"command": "go test ./..."},
		started: time.Now().Add(-3200 * time.Millisecond),
	}
	m.tools["b1"] = tb
	m.add(tb)
	m.handleEvent(claude.ToolProgress{ToolUseID: "b1", Output: "running pkg/foo\nok pkg/foo"})
	m.refresh()

	rendered := m.r.render(tb, 80)
	if !strings.Contains(rendered, "running pkg/foo") {
		t.Fatalf("expected live Bash output in running tool card, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "3.") {
		t.Fatalf("expected live elapsed timer (~3.2s) in running tool card, got:\n%s", rendered)
	}
}

func TestMinimalThemeAndCleanSidebar(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	th, ok := themeByName("minimal")
	if !ok || th.Name != "minimal" || !th.Dark {
		t.Fatalf("minimal theme not registered: %+v (ok=%v)", th, ok)
	}
	m := New(Config{Dark: true, Theme: "minimal"})
	t.Cleanup(func() { applyTheme(themes[0]) })
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.add(&block{kind: kindUser, text: "hello"})
	m.refresh()
	side := m.sidebar(m.vp.Height)
	if strings.Contains(side, "SHORTCUTS") {
		t.Fatalf("sidebar should omit redundant SHORTCUTS block:\n%s", side)
	}
	if strings.Contains(side, "saver") {
		t.Fatalf("sidebar should hide saver row when savers are off:\n%s", side)
	}
}

func BenchmarkEventBatchStream(b *testing.B) {
	b.Setenv("TEVEUS_CONFIG", b.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	for i := 0; i < 500; i++ {
		m.add(&block{kind: kindUser, text: fmt.Sprintf("q %d", i)})
		m.add(&block{kind: kindAssistant, text: "Some **markdown** reply with `code`."})
	}
	m.refresh()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := make(chan claude.Event, 32)
		for j := 0; j < 20; j++ {
			ch <- claude.TextDelta{Text: "tok "}
		}
		first := <-ch
		m.Update(eventMsg{gen: m.gen, ev: first, ch: ch})
		_ = m.View()
	}
}
