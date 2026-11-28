package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

type fakeBackend struct {
	claude.Backend
	interrupts int
}

func (f *fakeBackend) Interrupt() error { f.interrupts++; return nil }

func TestEscTwiceInterrupts(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	fb := &fakeBackend{}
	m.client, m.busy = fb, true
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if fb.interrupts != 0 || m.notice != "press esc again to interrupt" {
		t.Fatalf("first esc: interrupts=%d notice=%q", fb.interrupts, m.notice)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if fb.interrupts != 1 {
		t.Fatalf("second esc: interrupts=%d", fb.interrupts)
	}
	if !m.interrupting {
		t.Fatalf("expected m.interrupting=true after second esc")
	}
}

func TestEscWindowExtendedToThreeSeconds(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	fb := &fakeBackend{}
	m.client, m.busy = fb, true

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	// Simulate 2 seconds passing (previously >1.5s would reset the prompt).
	m.escAt = time.Now().Add(-2 * time.Second)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if fb.interrupts != 1 {
		t.Fatalf("second esc after 2s should interrupt, got interrupts=%d", fb.interrupts)
	}
}

func TestEscThirdPressForceCancelsStuckTool(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	fb := &fakeBackend{}
	m.client, m.busy = fb, true

	// Add a running Bash tool block that simulates a stuck subprocess.
	tb := &block{kind: kindTool, id: "t1", name: "Bash", state: toolRunning, started: time.Now()}
	m.tools["t1"] = tb
	m.add(tb)

	// 1st Esc: warn
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	// 2nd Esc: interrupt
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.busy || !m.interrupting {
		t.Fatalf("after 2nd esc: busy=%v interrupting=%v", m.busy, m.interrupting)
	}
	// 3rd Esc: force-cancel immediately
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.busy {
		t.Fatalf("after 3rd esc: expected m.busy=false")
	}
	if m.interrupting {
		t.Fatalf("after 3rd esc: expected m.interrupting=false")
	}
	if tb.state != toolFailed || tb.result != "cancelled" {
		t.Fatalf("expected running tool marked failed/cancelled, got state=%v result=%q", tb.state, tb.result)
	}
	if fb.interrupts != 2 {
		t.Fatalf("expected 2 Interrupt() calls (2nd + 3rd esc), got %d", fb.interrupts)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != kindInfo || !strings.Contains(last.text, "Interrupted") {
		t.Fatalf("expected Interrupted info block, got %+v", last)
	}
}

func TestEscStateResetsAfterTurnFinishes(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	fb := &fakeBackend{}
	m.client, m.busy = fb, true

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.interrupting {
		t.Fatalf("expected interrupting=true")
	}
	m.handleEvent(claude.Result{Subtype: "error_during_execution", IsError: true})
	if m.busy || m.interrupting {
		t.Fatalf("expected busy=false and interrupting=false after Result")
	}
	// Next turn should require double-Esc again, not force-cancel on 1st press.
	m.busy = true
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.busy || m.notice != "press esc again to interrupt" {
		t.Fatalf("new turn first esc: busy=%v notice=%q", m.busy, m.notice)
	}
}
