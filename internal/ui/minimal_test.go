package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

func TestSaversAreOffUntilTurnedOn(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	if o := m.applySavers(claude.Options{Model: "x"}); o.AppendPrompt != "" || len(o.DisallowTools) != 0 {
		t.Fatalf("savers on by default: %+v", o)
	}
	if got := m.saverLabel(); got != "off" {
		t.Fatalf("label %q", got)
	}
	m.engine = "claude" // lean tools apply to Claude Code only
	m.settings.Concise, m.settings.LeanTools, m.settings.MinimalCode = true, true, "full"
	o := m.applySavers(claude.Options{})
	if !strings.Contains(o.AppendPrompt, concisePrompt) || !strings.Contains(o.AppendPrompt, minimalFull) || len(o.DisallowTools) == 0 {
		t.Fatalf("turned on: %+v", o)
	}
	if got := m.saverLabel(); got != "concise · lean · minimal" {
		t.Fatalf("label %q", got)
	}
	for level, want := range map[string]string{"lite": minimalLite, "strict": "Treat every added line"} {
		m.settings.MinimalCode = level
		if p := m.applySavers(claude.Options{}).AppendPrompt; !strings.Contains(p, want) {
			t.Errorf("%s: %s", level, p)
		}
	}
	m.cfg.Full = true // -full turns every saver off
	if p := m.applySavers(claude.Options{}).AppendPrompt; p != "" {
		t.Fatalf("-full still appends %q", p)
	}
}

func TestMinimalSlider(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.settings.ClaudeNotice = true // changing the level restarts Claude Code
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.submit("/minimal")
	if m.pop.mode != popSlider || m.pop.sel != 0 {
		t.Fatalf("slider not open at off: mode=%v sel=%d", m.pop.mode, m.pop.sel)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	view := ansi.Strip(m.View())
	for _, want := range []string{"Minimal code", "off", "lite", "full", "strict", "◆", "reuse-first checklist"} {
		if !strings.Contains(view, want) {
			t.Fatalf("slider view lacks %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pop.open() || m.minimalLevel() != "full" || LoadSettings().MinimalCode != "full" {
		t.Fatalf("enter didn't save full: %s", m.minimalLevel())
	}
	// esc leaves the level alone; a typed level skips the slider.
	m.submit("/minimal")
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.minimalLevel() != "full" {
		t.Fatal("esc changed the level")
	}
	m.submit("/minimal strict")
	if m.pop.open() || m.minimalLevel() != "strict" {
		t.Fatal("/minimal strict didn't apply directly")
	}
	m.submit("/minimal bogus")
	if m.minimalLevel() != "strict" {
		t.Fatal("accepted an unknown level")
	}
}

func TestSliderFitsEveryWidth(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	for _, w := range []int{40, 60, 100, 200} {
		m := New(Config{Dark: true})
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		m.openArgPicker(claude.Command{Name: "effort", Description: "Set reasoning effort"}, []string{"low", "medium", "high", "xhigh", "max"})
		if m.pop.mode != popSlider {
			t.Fatal("/effort levels didn't get a slider")
		}
		for i, l := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %d is %d wide: %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
	if isScale([]string{"json", "text", "stream"}) || !isScale([]string{"low", "medium", "high"}) || isScale([]string{"high", "low", "max"}) {
		t.Fatal("isScale")
	}
}

func TestAttributionIsOffByDefault(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	if !m.startOptions(m.cfg.Claude).NoAttribution {
		t.Fatal("Claude Code would add Co-Authored-By by default")
	}
	m.toggleAttribution()
	if m.startOptions(m.cfg.Claude).NoAttribution || !LoadSettings().Attribution {
		t.Fatal("turning it on didn't stick")
	}
}

func TestMinimalPromptStatesPrinciplesNotCases(t *testing.T) {
	// The first version of this named the benchmark's own examples, which
	// would only ever fix those. The rules have to be general.
	for _, level := range []string{"full", "strict"} {
		p := strings.ToLower(minimalPrompt(level))
		for _, want := range []string{
			"as asked",         // add what the request asks for, in that form
			"exported",         // public names are a promise
			"standard-library", // a library call beats a hand-written shape
		} {
			if !strings.Contains(p, want) {
				t.Fatalf("%s prompt lacks the rule about %q", level, want)
			}
		}
		// Naming one caller's own functions would tie the rule to that case.
		for _, leak := range []string{"div", "errdiv", "wordcount", "builder", "closure"} {
			if strings.Contains(p, leak) {
				t.Fatalf("%s prompt names the benchmark's case %q instead of a rule", level, leak)
			}
		}
		// Never cut what keeps code correct.
		for _, keep := range []string{"validation", "security", "accessibility"} {
			if !strings.Contains(p, keep) {
				t.Fatalf("%s dropped %q", level, keep)
			}
		}
	}
	// Strict must also protect against cutting too much: lean is not broken.
	if st := strings.ToLower(minimalPrompt("strict")); !strings.Contains(st, "still compiles") {
		t.Fatal("strict doesn't ask for the lean result to still work")
	}
}

func TestMinimalPromptHandlesAmbiguity(t *testing.T) {
	// Reading a request two ways is how the model grew a WordCount that
	// returned a tally. The rule has to say which reading wins, generally.
	for _, level := range []string{"full", "strict"} {
		p := strings.ToLower(minimalPrompt(level))
		if !strings.Contains(p, "two ways") {
			t.Fatalf("%s doesn't mention ambiguous requests", level)
		}
		if !strings.Contains(p, "simpler value") {
			t.Fatalf("%s doesn't say the simpler reading wins", level)
		}
	}
}
