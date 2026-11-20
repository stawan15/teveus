package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// lead is how many blank cells a row starts with, in the transcript.
func lead(s string) int {
	p := ansi.Strip(s)
	return len(p) - len(strings.TrimLeft(p, " "))
}

// Every kind of block starts in the same column. Before, prose began at the
// gutter while a tool call added two more spaces and info blocks one, so the
// transcript's left edge was ragged.
func TestBlocksShareOneLeftEdge(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	r := &renderer{cwd: "/tmp"}
	for name, b := range map[string]*block{
		"prose":   {kind: kindAssistant, text: "a plain sentence"},
		"heading": {kind: kindAssistant, text: "## a heading\n\nand a paragraph"},
		"list":    {kind: kindAssistant, text: "- one\n- two"},
		"code":    {kind: kindAssistant, text: "```go\nfunc x() {}\n```"},
		"tool":    {kind: kindTool, name: "Bash", input: map[string]any{"command": "ls"}, state: toolDone},
		"info":    {kind: kindInfo, text: "a note"},
		"error":   {kind: kindError, text: "it broke"},
		"turnend": {kind: kindTurnEnd, text: "done"},
	} {
		out := r.render(b, 60)
		for _, l := range strings.Split(out, "\n") {
			if strings.TrimSpace(ansi.Strip(l)) == "" {
				continue
			}
			if got := lead(l); got != 0 {
				t.Errorf("%s starts at column %d: %q", name, got, ansi.Strip(l))
			}
			break
		}
	}
}

// glamour indents each block by its own margin on top of the document's, so
// headings, prose, tables and code each started at a different column.
func TestMarkdownBlocksShareOneEdge(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	r := &renderer{cwd: "/tmp"}
	md := "## heading\n\napart\n\n- item\n\n1. first\n\n```go\nx := 1\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	for _, l := range strings.Split(r.markdown(md, 60), "\n") {
		p := ansi.Strip(l)
		// A table's cells carry their own one-cell padding inside the border,
		// which is part of the table rather than the block's left edge.
		if strings.TrimSpace(p) == "" || strings.Contains(p, "│") || strings.Contains(p, "┼") {
			continue
		}
		if got := lead(l); got != 0 {
			t.Errorf("column %d: %q", got, strings.TrimRight(p, " "))
		}
	}
}

// The frame itself still comes out exactly as wide as the window, which is
// what keeps the sidebar's border in one place.
func TestFrameKeepsTheSidebarEdge(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m.add(&block{kind: kindUser, text: "hi"})
	m.add(&block{kind: kindAssistant, text: "## heading\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"})
	m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "ls"}, state: toolDone})
	m.refresh()
	for i, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w != m.w {
			t.Fatalf("row %d is %d wide, want %d: %q", i, w, m.w, ansi.Strip(l))
		}
	}
}
