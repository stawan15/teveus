package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// A streamed chunk only redraws the block it changed, so the transcript built
// piecemeal has to match the one a full redraw produces.
func TestIncrementalRefreshMatchesFullRedraw(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	kinds := []blockKind{kindUser, kindAssistant, kindTool, kindInfo, kindError, kindTurnEnd, kindCmdOut}
	for i := 0; i < 40; i++ {
		m.add(&block{kind: kinds[i%len(kinds)], text: fmt.Sprintf("block %d\n\nwith `code`\n", i),
			name: "Bash", input: map[string]any{"command": "go test ./..."}, result: "ok", state: toolDone})
		m.refresh()
	}
	m.add(&block{kind: kindAssistant, streaming: true})
	for _, chunk := range []string{"first ", "words\n", "and a second line..."} {
		b := m.blocks[len(m.blocks)-1]
		b.text += chunk
		b.invalidate()
		m.refresh()
	}
	// The lines the incremental pass kept have to be the ones a full rebuild
	// would draw, and the blocks have to start where it started them.
	gotLines, gotStarts := append([]string(nil), m.lines...), append([]int(nil), m.blockStart...)
	m.tc = prefixCache{}
	m.refresh()
	if strings.Join(gotLines, "\n") != strings.Join(m.lines, "\n") {
		t.Fatalf("incremental transcript differs from a full redraw")
	}
	if fmt.Sprint(gotStarts) != fmt.Sprint(m.blockStart) {
		t.Fatalf("block starts %v, full redraw %v", gotStarts, m.blockStart)
	}
	// A resize has to redraw at the new width, not reuse the old lines.
	m.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	m.refresh()
	gotLines = append([]string(nil), m.lines...)
	m.tc = prefixCache{}
	m.refresh()
	if strings.Join(gotLines, "\n") != strings.Join(m.lines, "\n") {
		t.Fatalf("transcript after a resize differs from a full redraw")
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "second line") {
		t.Fatalf("transcript missing from the screen:\n%s", got)
	}
}
