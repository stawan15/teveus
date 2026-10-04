package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

func TestCSIKeys(t *testing.T) {
	for seq, want := range map[string]string{
		"\x1b[13;2u":    "ctrl+j", // shift+enter: newline
		"\x1b[27;2;13~": "ctrl+j", // same, xterm modifyOtherKeys
		"\x1b[13;5u":    "ctrl+j",
		"\x1b[13;3u":    "alt+enter",
		"\x1b[99;5u":    "ctrl+c",
		"\x1b[27u":      "esc",
		"\x1b[9;2u":     "shift+tab",
		"\x1b[107;5u":   "ctrl+k",
		"\x1b[98;3u":    "alt+b",
		"\x1b[127;3u":   "alt+backspace",
		"\x1b[106;5:1u": "ctrl+j", // with an event type
		"\x1b[99;9u":    "",       // super+c: the terminal's, not ours
		"\x1b[1;5A":     "",
	} {
		k, ok := csiKey([]byte(seq))
		got := ""
		if ok {
			got = k.String()
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", seq, got, want)
		}
	}
}

func TestShiftEnterInsertsNewline(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	k, _ := csiKey([]byte("\x1b[13;2u"))
	m.Update(k)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if v := m.input.Value(); v != "a\nb" {
		t.Fatalf("input = %q", v)
	}
	// A trailing backslash does the same where shift+enter can't be told apart.
	m.input.SetValue("c\\")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := m.input.Value(); v != "c\n" {
		t.Fatalf("backslash enter: input = %q", v)
	}
}

type sendBackend struct {
	claude.Backend
	sent []string
}

func (s *sendBackend) Send(text string) error { s.sent = append(s.sent, text); return nil }

func TestLongPasteCollapses(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sb := &sendBackend{}
	m.client = sb
	code := strings.Repeat("fmt.Println(1)\n", 30)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix this "), Paste: false})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(code), Paste: true})
	if v := m.input.Value(); v != "fix this [Pasted text #1 +30 lines]" {
		t.Fatalf("input = %q", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(sb.sent) != 1 || sb.sent[0] != strings.TrimSpace("fix this "+code) {
		t.Fatalf("sent %q", sb.sent)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != kindUser || last.text != "fix this [Pasted text #1 +30 lines]" {
		t.Fatalf("transcript shows %q", last.text)
	}
	// Short pastes go in as they are.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("two\nlines"), Paste: true})
	if v := m.input.Value(); v != "two\nlines" {
		t.Fatalf("short paste: %q", v)
	}
}

func TestTurnsAreSeparatedByARule(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.add(&block{kind: kindUser, text: "one"})
	m.add(&block{kind: kindTurnEnd, text: "done"})
	m.add(&block{kind: kindUser, text: "two"})
	m.refresh()
	rules := 0
	for _, l := range m.lines {
		if strings.Contains(ansi.Strip(l), "────────") {
			rules++
		}
	}
	if rules != 1 {
		t.Fatalf("%d rules between two turns", rules)
	}
}

func TestShiftArrowsScrollAndPlainArrowsBrowseHistory(t *testing.T) {
	m, _ := newSelModel(t)
	m.history = []string{"earlier prompt"}
	m.vp.GotoBottom()
	at := m.vp.YOffset
	m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if m.vp.YOffset >= at || m.input.Value() != "" {
		t.Fatalf("shift+up: offset %d -> %d, input %q", at, m.vp.YOffset, m.input.Value())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if m.vp.YOffset != at {
		t.Fatalf("shift+down didn't scroll back: %d", m.vp.YOffset)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.input.Value() != "earlier prompt" || m.vp.YOffset != at {
		t.Fatalf("↑ should recall history only: input %q, offset %d", m.input.Value(), m.vp.YOffset)
	}
}

func TestSplitKeysJoinsMouseReport(t *testing.T) {
	for _, first := range []string{"\x1b", "\x1b[", "\x1b[<65;28;2"} {
		rd, wr, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		const report = "\x1b[<65;28;24M"
		go func() {
			wr.WriteString(first)
			time.Sleep(10 * time.Millisecond)
			wr.WriteString(report[len(first):])
		}()
		in := &splitKeys{File: rd, ready: func(time.Duration) bool { return true }}
		buf := make([]byte, 256)
		n, err := in.Read(buf)
		if err != nil || string(buf[:n]) != report {
			t.Errorf("%q first: got %q, %v", first, buf[:n], err)
		}
		rd.Close()
		wr.Close()
	}
}

func TestSplitKeysHoldsReportCutByFullBuffer(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	const report = "\x1b[<65;28;24M"
	all := strings.Repeat(report, 30) // 360 bytes: the 256-byte buffer ends mid-report
	wr.WriteString(all)
	wr.Close()
	in := &splitKeys{File: rd, ready: func(time.Duration) bool { return true }}
	buf := make([]byte, 256)
	var got string
	for len(got) < len(all) {
		n, err := in.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(buf[:n]), "M") {
			t.Fatalf("read ends mid-report: %q", buf[:n])
		}
		got += string(buf[:n])
	}
	if got != all {
		t.Fatalf("lost input: got %d bytes, want %d", len(got), len(all))
	}
}

func TestEmptyPasteFetchesClipboardImage(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Paste: true})
	if cmd == nil {
		t.Fatal("empty paste should look for a clipboard image")
	}
}
