package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

type savedSession struct {
	engine  string // "claude" or "api"
	id      string
	title   string
	model   string
	updated time.Time
	path    string
}

func sessionDir() string { return filepath.Join(configDir(), "sessions") }

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeProjectDir is where Claude Code keeps this directory's sessions.
func claudeProjectDir(cwd string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects", nonAlnum.ReplaceAllString(cwd, "-"))
}

type jsonlLine struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// userText returns a real typed prompt, skipping tool results and the
// command/attachment wrappers Claude Code records.
func (l jsonlLine) userText() string {
	if l.Type != "user" || l.IsSidechain || l.IsMeta {
		return ""
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) != nil {
		var blocks []claude.ContentBlock
		json.Unmarshal(l.Message.Content, &blocks)
		for _, b := range blocks {
			if b.Type == "text" {
				s = b.Text
			}
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

func scanJSONL(path string, each func(jsonlLine) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		var l jsonlLine
		if json.Unmarshal(sc.Bytes(), &l) == nil && !each(l) {
			return
		}
	}
}

func listClaudeSessions(cwd string) []savedSession {
	dir := claudeProjectDir(cwd)
	entries, _ := os.ReadDir(dir)
	var out []savedSession
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		s := savedSession{engine: "claude", id: strings.TrimSuffix(e.Name(), ".jsonl"),
			updated: info.ModTime(), path: filepath.Join(dir, e.Name())}
		scanJSONL(s.path, func(l jsonlLine) bool {
			if s.title == "" {
				s.title = l.userText()
			}
			if l.Type == "assistant" && l.Message.Model != "" {
				s.model = l.Message.Model
			}
			return s.title == "" || s.model == ""
		})
		if s.title != "" {
			out = append(out, s)
		}
	}
	return out
}

func listAllSessions(cwd string) []savedSession {
	out := listClaudeSessions(cwd)
	for _, s := range agent.ListSessions(sessionDir(), cwd) {
		out = append(out, savedSession{engine: "api", id: s.ID, title: s.Title, model: s.Model, updated: s.Updated})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].updated.After(out[j].updated) })
	return out
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2 Jan")
}

func (m *Model) openResumePicker() {
	sessions := listAllSessions(m.cwd)
	if len(sessions) == 0 {
		m.note("no earlier conversations in this folder", false)
		return
	}
	var cs []choice
	for _, s := range sessions {
		s := s
		group := "Claude Code"
		if s.engine == "api" {
			group = "Direct API"
		}
		cs = append(cs, choice{label: s.title, value: s.engine + ":" + s.id, desc: shortModel(s.model),
			key: ago(s.updated), group: group, run: func(m *Model) tea.Cmd { return m.resumeSession(s) }})
	}
	// Newest first across both engines; the group shows in each row instead.
	m.openPicker("Resume a conversation", cs, 0)
	m.pop.flat = true
}

// resumeSession reopens a conversation on its engine and shows its history.
func (m *Model) resumeSession(s savedSession) tea.Cmd {
	if m.busy {
		m.note("finish or interrupt the current turn first", false)
		return nil
	}
	if m.client != nil {
		m.client.Close()
	}
	if m.engine != s.engine {
		m.engine = s.engine
		m.settings.Engine = s.engine
		saveSettings(m.settings)
		m.commands, m.models = nil, nil
	}
	m.blocks, m.tools, m.stream, m.todos, m.tasks = nil, map[string]*block{}, nil, nil, nil
	m.busy, m.perms, m.sessionID = false, nil, s.id
	m.cost, m.prevCost, m.context, m.warnedCtx, m.tokIn, m.tokOut = 0, 0, 0, false, 0, 0
	m.promptedModel, m.noProvider = true, nil

	if s.engine == "api" {
		if sess, err := agent.LoadSession(sessionDir(), s.id); err == nil {
			m.replayAPI(sess.History)
			if sess.Model != "" {
				m.model = sess.Model
			}
		}
	} else {
		m.replayClaude(s.path)
	}
	opts := m.cfg.Claude
	opts.Resume, opts.Continue = s.id, false
	cmd := m.start(opts)
	m.add(&block{kind: kindInfo, text: "Resumed · " + ago(s.updated)})
	m.refresh()
	m.vp.GotoBottom()
	return cmd
}

func (m *Model) replayAPI(history []agent.Message) {
	for _, msg := range history {
		switch msg.Role {
		case "user":
			m.add(&block{kind: kindUser, text: msg.Text})
		case "assistant":
			if strings.TrimSpace(msg.Text) != "" {
				m.add(&block{kind: kindAssistant, text: msg.Text})
			}
			for _, tc := range msg.ToolCalls {
				m.addReplayTool(tc.ID, tc.Name, tc.Input)
			}
		case "tool":
			m.finishReplayTool(msg.ToolCallID, msg.Result, msg.IsError)
		}
	}
}

func (m *Model) replayClaude(path string) {
	scanJSONL(path, func(l jsonlLine) bool {
		if l.IsSidechain {
			return true
		}
		switch l.Type {
		case "user":
			if t := l.userText(); t != "" {
				m.add(&block{kind: kindUser, text: t})
				return true
			}
			var blocks []claude.ContentBlock
			json.Unmarshal(l.Message.Content, &blocks)
			for _, b := range blocks {
				if b.Type == "tool_result" {
					m.finishReplayTool(b.ToolUseID, b.ResultText(), b.IsError)
				}
			}
		case "assistant":
			var blocks []claude.ContentBlock
			json.Unmarshal(l.Message.Content, &blocks)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						m.add(&block{kind: kindAssistant, text: b.Text})
					}
				case "tool_use":
					m.addReplayTool(b.ID, b.Name, b.Input)
				}
			}
		}
		return true
	})
}

func (m *Model) addReplayTool(id, name string, input json.RawMessage) {
	var in map[string]any
	json.Unmarshal(input, &in)
	b := &block{kind: kindTool, id: id, name: name, input: in, state: toolDone}
	m.tools[id] = b
	m.add(b)
}

func (m *Model) finishReplayTool(id, result string, isErr bool) {
	if b := m.tools[id]; b != nil {
		b.result = result
		if isErr {
			b.state = toolFailed
		}
	}
}
