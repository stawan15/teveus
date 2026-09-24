package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

const maxSessions = 9

// A session is one conversation with its own backend. The one on screen keeps
// its state in the Model's fields; the others are parked here and keep
// running, and their events are applied by swapping them in for a moment.
type session struct {
	client claude.Backend
	engine string
	gen    int

	blocks   []*block
	tools    map[string]*block
	stream   *block
	todos    map[string]any
	tasks    []task
	turnFrom int

	busy      bool
	phase     string
	turnStart time.Time
	perms     []*claude.PermissionRequest

	sessionID, model, mode   string
	cost, prevCost           float64
	context                  int
	commands                 []claude.Command
	models                   []claude.ModelInfo
	warnedCtx, promptedModel bool
	noProvider               *block
	tokIn, tokOut            int

	draft  string
	scroll int
	follow bool
}

func (m *Model) save() {
	s := m.sessions[m.cur]
	s.client, s.engine, s.gen = m.client, m.engine, m.gen
	s.blocks, s.tools, s.stream, s.todos, s.tasks, s.turnFrom = m.blocks, m.tools, m.stream, m.todos, m.tasks, m.turnFrom
	s.busy, s.phase, s.turnStart, s.perms = m.busy, m.phase, m.turnStart, m.perms
	s.sessionID, s.model, s.mode = m.sessionID, m.model, m.mode
	s.cost, s.prevCost, s.context = m.cost, m.prevCost, m.context
	s.commands, s.models = m.commands, m.models
	s.warnedCtx, s.promptedModel, s.noProvider = m.warnedCtx, m.promptedModel, m.noProvider
	s.tokIn, s.tokOut = m.tokIn, m.tokOut
}

func (m *Model) load() {
	s := m.sessions[m.cur]
	m.client, m.engine, m.gen = s.client, s.engine, s.gen
	m.blocks, m.tools, m.stream, m.todos, m.tasks, m.turnFrom = s.blocks, s.tools, s.stream, s.todos, s.tasks, s.turnFrom
	m.busy, m.phase, m.turnStart, m.perms = s.busy, s.phase, s.turnStart, s.perms
	m.sessionID, m.model, m.mode = s.sessionID, s.model, s.mode
	m.cost, m.prevCost, m.context = s.cost, s.prevCost, s.context
	m.commands, m.models = s.commands, s.models
	m.warnedCtx, m.promptedModel, m.noProvider = s.warnedCtx, s.promptedModel, s.noProvider
	m.tokIn, m.tokOut = s.tokIn, s.tokOut
}

// backgroundEvent applies an event to a session that isn't on screen.
func (m *Model) backgroundEvent(i int, ev claude.Event) {
	pop, notice, noticeOK, noticeAt := m.pop, m.notice, m.noticeOK, m.noticeAt
	cur := m.cur
	m.save()
	m.cur = i
	m.load()
	m.handleEvent(ev)
	m.save()
	m.cur = cur
	m.load()
	m.pop, m.notice, m.noticeOK, m.noticeAt = pop, notice, noticeOK, noticeAt
}

// sessionOf finds the parked session a backend generation belongs to.
func (m *Model) sessionOf(gen int) (int, bool) {
	for i, s := range m.sessions {
		if i != m.cur && s.gen == gen && gen != 0 {
			return i, true
		}
	}
	return 0, false
}

func sessionTitle(blocks []*block) string {
	for _, b := range blocks {
		if b.kind == kindUser {
			t := strings.Join(strings.Fields(b.text), " ")
			if r := []rune(t); len(r) > 40 {
				t = string(r[:40]) + "…"
			}
			return t
		}
	}
	return "New session"
}

func (m *Model) sessionState(i int) (title string, busy, waiting bool) {
	if i == m.cur {
		return sessionTitle(m.blocks), m.busy, len(m.perms) > 0
	}
	s := m.sessions[i]
	return sessionTitle(s.blocks), s.busy, len(s.perms) > 0
}

// tabStrip is one line under the header, shown only with two or more
// sessions: ● marks one that is working, ! one waiting for approval.
func (m *Model) tabStrip() string {
	n := len(m.sessions)
	if n < 2 {
		return ""
	}
	label := max(min((m.w-2)/n, 26)-8, 3)
	var sb strings.Builder
	sb.WriteString(" ")
	for i := range m.sessions {
		title, busy, waiting := m.sessionState(i)
		if r := []rune(title); len(r) > label {
			title = string(r[:label-1]) + "…"
		}
		num := strconv.Itoa(i + 1)
		cell := sFaint.Render(num) + " " + sDim.Render(title)
		if i == m.cur {
			cell = sAccent.Bold(true).Render(num) + " " + sText.Bold(true).Render(title)
		}
		switch {
		case waiting:
			cell += " " + sYellow.Bold(true).Render("!")
		case busy:
			cell += " " + sAccent.Render("●")
		}
		sb.WriteString(cell + "   ")
	}
	return ansi.Truncate(sb.String(), m.w, "")
}

func (m *Model) tabsHeight() int {
	if len(m.sessions) > 1 {
		return 1
	}
	return 0
}

func (m *Model) rememberView() {
	s := m.sessions[m.cur]
	s.draft, s.scroll, s.follow = m.input.Value(), m.vp.YOffset, m.vp.AtBottom()
}

func (m *Model) showView(s *session) {
	m.pop.close()
	m.clearSelection()
	m.histIdx = -1
	m.input.SetValue(s.draft)
	m.layout()
	m.refresh()
	if s.follow {
		m.vp.GotoBottom()
	} else {
		m.vp.setYOffset(s.scroll)
	}
	m.afterInput()
}

func (m *Model) switchTo(i int) tea.Cmd {
	if i == m.cur || i < 0 || i >= len(m.sessions) {
		return nil
	}
	m.save()
	m.rememberView()
	m.cur = i
	m.load()
	m.showView(m.sessions[i])
	return nil
}

func (m *Model) newSession() tea.Cmd {
	switch {
	case m.engine == "":
		m.note("connect an AI first: /login", false)
		return nil
	case len(m.sessions) >= maxSessions:
		m.note(fmt.Sprintf("at most %d sessions: close one first", maxSessions), false)
		return nil
	}
	m.save()
	m.rememberView()
	m.sessions = append(m.sessions, &session{follow: true})
	m.cur = len(m.sessions) - 1
	m.client, m.gen = nil, 0
	m.resetConversation()
	opts := m.cfg.Claude
	opts.Resume, opts.Continue = "", false
	cmd := m.start(opts)
	m.showView(m.sessions[m.cur])
	m.note(fmt.Sprintf("session %d", m.cur+1), true)
	return cmd
}

func (m *Model) closeSession() tea.Cmd {
	if len(m.sessions) < 2 {
		m.note("this is the only session: /exit to quit", false)
		return nil
	}
	if m.client != nil {
		m.client.Close()
	}
	i := m.cur
	m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
	m.cur = min(i, len(m.sessions)-1)
	m.load()
	m.showView(m.sessions[m.cur])
	m.note(fmt.Sprintf("closed session %d", i+1), true)
	return nil
}

func (m *Model) openSessions() {
	var cs []choice
	for i := range m.sessions {
		i := i
		title, busy, waiting := m.sessionState(i)
		state := "idle"
		switch {
		case waiting:
			state = "needs your approval"
		case busy:
			state = "working"
		}
		key := "alt+" + strconv.Itoa(i+1)
		if i == m.cur {
			key = "this one"
		}
		cs = append(cs, choice{label: strconv.Itoa(i+1) + "  " + title, desc: state, key: key,
			run: func(m *Model) tea.Cmd { return m.switchTo(i) }})
	}
	cs = append(cs, choice{label: "+  New session", desc: "start another conversation; this one keeps running",
		run: func(m *Model) tea.Cmd { return m.newSession() }})
	if len(m.sessions) > 1 {
		cs = append(cs, choice{label: "×  Close this session", desc: "stop and remove session " + strconv.Itoa(m.cur+1),
			run: func(m *Model) tea.Cmd { return m.closeSession() }})
	}
	m.openPicker("Sessions", cs, m.cur)
	m.pop.flat = true
}

func (m *Model) closeAllSessions() {
	m.save()
	for _, s := range m.sessions {
		if s.client != nil {
			s.client.Close()
		}
	}
}
