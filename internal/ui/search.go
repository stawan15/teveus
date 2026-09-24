package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

const (
	searchThisMax  = 20 // matches listed from the open conversation
	searchOtherMax = 20 // earlier conversations listed
)

type searchHit struct {
	s       savedSession
	snippet string
	n       int // messages in that conversation that match
}

type searchMsg struct {
	query string
	hits  []searchHit
}

// search finds text in this conversation and in the earlier ones saved for
// this folder. Picking a match here scrolls to it; picking an earlier
// conversation resumes it.
func (m *Model) search(arg string) tea.Cmd {
	q := strings.TrimSpace(arg)
	if q == "" {
		m.input.SetValue("/search ")
		m.afterInput()
		return nil
	}
	cwd, current := m.cwd, m.sessionID
	m.note("searching…", true)
	return func() tea.Msg {
		var hits []searchHit
		for _, s := range listAllSessions(cwd) {
			if s.id == current {
				continue // its messages are the open conversation's
			}
			if h, ok := searchSession(s, q); ok {
				if hits = append(hits, h); len(hits) == searchOtherMax {
					break
				}
			}
		}
		return searchMsg{q, hits}
	}
}

// showSearch lists the matches: the open conversation's first.
func (m *Model) showSearch(msg searchMsg) {
	var cs []choice
	for i := len(m.blocks) - 1; i >= 0 && len(cs) < searchThisMax; i-- {
		b := m.blocks[i]
		if b.kind != kindUser && b.kind != kindAssistant {
			continue
		}
		snip, ok := snippet(b.text, msg.query)
		if !ok {
			continue
		}
		who := "you"
		if b.kind == kindAssistant {
			who = "reply"
		}
		i := i
		cs = append(cs, choice{label: snip, desc: who, group: "This conversation", run: func(m *Model) tea.Cmd {
			if i < len(m.blockStart) {
				m.vp.setYOffset(m.blockStart[i] - 1)
			}
			return nil
		}})
	}
	for _, h := range msg.hits {
		h := h
		desc := h.s.title
		if h.n > 1 {
			desc = fmt.Sprintf("%s · %d matches", desc, h.n)
		}
		cs = append(cs, choice{label: h.snippet, desc: desc, key: ago(h.s.updated), group: "Earlier conversations",
			run: func(m *Model) tea.Cmd { return m.resumeSession(h.s) }})
	}
	if len(cs) == 0 {
		m.note("no matches for "+msg.query, false)
		return
	}
	m.openPicker("Search · "+msg.query, cs, 0)
}

// searchSession looks through what was said in a saved conversation: your
// messages and the replies, not tool output.
func searchSession(s savedSession, query string) (searchHit, bool) {
	h := searchHit{s: s}
	see := func(text string) {
		if snip, ok := snippet(text, query); ok {
			if h.n == 0 {
				h.snippet = snip
			}
			h.n++
		}
	}
	if s.engine == "api" {
		sess, err := agent.LoadSession(sessionDir(), s.id)
		if err != nil {
			return h, false
		}
		for _, msg := range sess.History {
			if msg.Role == "user" || msg.Role == "assistant" {
				see(msg.Text)
			}
		}
	} else {
		scanJSONL(s.path, func(l jsonlLine) bool {
			if l.IsSidechain {
				return true
			}
			if t := l.userText(); t != "" {
				see(t)
			} else if l.Type == "assistant" {
				var blocks []claude.ContentBlock
				json.Unmarshal(l.Message.Content, &blocks)
				for _, b := range blocks {
					if b.Type == "text" {
						see(b.Text)
					}
				}
			}
			return true
		})
	}
	return h, h.n > 0
}

// snippet returns, on one line, the text around the first case-insensitive
// match of query, or false if there is none.
func snippet(text, query string) (string, bool) {
	lower := func(s string) string { return strings.Map(unicode.ToLower, s) }
	flat := strings.Join(strings.Fields(text), " ")
	folded := lower(flat)
	at := strings.Index(folded, lower(query))
	if at < 0 {
		return "", false
	}
	// Lowering keeps the rune count, so a rune offset in folded is one in flat.
	from := utf8.RuneCountInString(folded[:at])
	runes := []rune(flat)
	start := max(from-30, 0)
	end := min(from+utf8.RuneCountInString(query)+50, len(runes))
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out, true
}
