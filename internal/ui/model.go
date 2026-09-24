// Package ui is the Bubble Tea front end.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

type Config struct {
	Claude   claude.Options
	Dark     bool
	Theme    string // overrides the saved theme when set
	Settings Settings
	Full     bool   // disable the usage savers for this run
	Engine   string // overrides the saved engine when set
	Onboard  bool   // show first-run setup (set by main when it hasn't run yet)
	Version  string
	KeyOut   io.Writer // the terminal, for key-protocol escapes; nil in tests
	Headless bool      // teveus -p: no UI to ask in
}

var modes = []string{"default", "acceptEdits", "plan", "auto"}

type Model struct {
	cfg        Config
	pastes     map[int]string // collapsed pastes by number
	pasteN     int
	pasteShown string               // what the next sent user turn shows, when pastes were collapsed
	images     map[int]claude.Image // attached pictures by number
	imageN     int
	outImages  []claude.Image // pictures going with the next sent user turn
	mcpNoted   bool
	settings   Settings
	client     claude.Backend
	engine     string
	gen        int // this session's backend generation: events from any other are ignored or belong to a parked session
	genSeq     int // last generation handed out, across all sessions
	sessions   []*session
	cur        int    // index in sessions of the one on screen
	home       string // the folder teveus was started in; new sessions start there
	homeBranch string
	bg         bool // handling an event of a session that isn't on screen

	w, h         int
	chatW, sideW int
	vp           scroller
	input        textarea.Model
	r            *renderer
	frame        int
	blockStart   []int    // first transcript line of each block, for mouse clicks
	lines        []string // rendered transcript lines, for selection
	sel          selection

	blocks   []*block
	tools    map[string]*block
	stream   *block
	todos    map[string]any
	tasks    []task
	turnFrom int // index of the first block of the running turn

	busy      bool
	phase     string
	turnStart time.Time
	perms     []*claude.PermissionRequest

	sessionID string
	model     string
	mode      string
	cwd       string
	branch    string
	cost      float64
	prevCost  float64
	context   int
	fiveHour  *claude.Window
	sevenDay  *claude.Window
	commands  []claude.Command
	models    []claude.ModelInfo
	files     []string

	pop           popup
	warnedCtx     bool
	promptedModel bool
	claudeAuth    claudeAuthMsg
	noProvider    *block
	tokIn, tokOut int
	history       []string
	histIdx       int
	draft         string
	ctrlC         time.Time
	escAt         time.Time
	notice        string
	noticeOK      bool
	noticeAt      time.Time
}

type eventMsg struct {
	gen int
	ev  claude.Event
	ch  <-chan claude.Event
}

type tickMsg struct{}

func New(cfg Config) *Model {
	tightenConfig()
	ta := textarea.New()
	ta.Placeholder = "Message…"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "alt+enter"))
	ta.Focus()

	settings := cfg.Settings
	name := settings.Theme
	if cfg.Theme != "" {
		name = cfg.Theme
	}
	t, ok := themeByName(name)
	if !ok && !cfg.Dark {
		t, _ = themeByName("light")
	}
	applyTheme(t)
	styleInput(&ta)

	cwd := cfg.Claude.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	mode := cfg.Claude.PermissionMode
	if mode == "" {
		mode = "default"
	}
	engine := settings.Engine
	if cfg.Engine != "" {
		engine = cfg.Engine
	}
	// Nothing is connected until the user picks an engine ("" = none).
	// Claude Code only runs after its notice was accepted; a saved API engine
	// with no key left starts disconnected rather than showing errors. An
	// explicit -engine flag is always honoured (Claude Code still asks first).
	switch {
	case engine != "api" && engine != "claude":
		engine = ""
	case cfg.Engine != "":
	case engine == "claude" && !settings.ClaudeNotice:
		engine = ""
	case engine == "api" && !hasAPICredentials():
		engine = ""
	}
	model := cfg.Claude.Model
	if engine == "api" && model == "" {
		model = settings.APIModel
	}
	vp := scroller{MouseWheelDelta: 3}
	return &Model{
		cfg:        cfg,
		sessions:   []*session{{follow: true}},
		settings:   settings,
		vp:         vp,
		input:      ta,
		r:          &renderer{cwd: cwd, spin: spinFrames[0], expand: settings.ToolDetail},
		tools:      map[string]*block{},
		cwd:        cwd,
		home:       cwd,
		branch:     gitBranch(cwd),
		homeBranch: gitBranch(cwd),
		mode:       mode,
		model:      model,
		engine:     engine,
		history:    loadHistory(),
		histIdx:    -1,
	}
}

func styleInput(ta *textarea.Model) {
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = sFaint
	ta.FocusedStyle.Text = sText
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.Cursor.Style = sAccent
}

func (m *Model) Init() tea.Cmd {
	m.setKittyKeys(true)
	return tea.Batch(textarea.Blink, tick(), m.start(m.cfg.Claude), indexFiles(m.cwd), checkClaudeAuth(m.cfg.Claude.Binary), m.pruneSessions(), m.startupUpdateCheck())
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func tick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func listen(gen int, ch <-chan claude.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg{gen: gen, ev: ev, ch: ch}
	}
}

// startOptions is what a new backend starts with: the savers, mode, model
// and effort the user picked.
func (m *Model) startOptions(opts claude.Options) claude.Options {
	opts = m.applySavers(opts)
	opts.PermissionMode = m.mode
	opts.Effort = m.settings.Effort
	opts.NoAttribution = !m.settings.Attribution
	if m.model != "" {
		opts.Model = m.model
	}
	opts.Cwd = m.cwd // a session may work in a worktree
	return opts
}

func (m *Model) start(opts claude.Options) tea.Cmd {
	if m.engine == "" {
		return nil // not connected yet: /login
	}
	if m.gateClaude(opts) {
		return nil
	}
	opts = m.startOptions(opts)
	var c claude.Backend
	var err error
	if m.engine == "api" {
		c, err = agent.Start(agent.Options{
			Cwd: m.cwd, Model: opts.Model, Mode: opts.PermissionMode,
			Store: agent.NewStore(configDir()), Style: opts.AppendPrompt,
			SessionDir: sessionDir(), Resume: opts.Resume, ConfigDir: configDir(), Effort: opts.Effort, Attribution: m.settings.Attribution, SubagentModel: m.settings.SubagentModel,
		})
	} else {
		c, err = claude.Start(opts)
	}
	if err != nil {
		m.add(&block{kind: kindError, text: err.Error()})
		return nil
	}
	m.client = c
	m.genSeq++
	m.gen = m.genSeq
	m.noteProjectMCP()
	return listen(m.gen, c.Events())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		m.refresh()
		return m, nil

	case tickMsg:
		m.autoScroll()
		m.frame++
		m.r.spin = spinFrames[m.frame%len(spinFrames)]
		if m.settings.ReduceMotion {
			m.r.spin = "●"
		}
		if m.busy {
			for _, b := range m.blocks {
				if b.kind == kindTool && b.state == toolRunning {
					b.invalidate()
				}
			}
			m.refresh()
		}
		return m, tick()

	case filesMsg:
		m.files = msg
		return m, nil

	case claudeAuthMsg:
		m.claudeAuth = msg
		// Setup waits for the login check so it can say whether Claude is connected.
		if m.cfg.Onboard && !m.settings.Onboarded && !m.pop.open() && len(m.perms) == 0 {
			m.cfg.Onboard = false
			m.startOnboarding()
		}
		return m, nil

	case shellResultMsg:
		m.handleShell(msg)
		return m, nil

	case worktreeMsg:
		if msg.err != nil {
			m.note(msg.err.Error(), false)
			return m, nil
		}
		return m, m.addSession(msg.cwd, msg.name, &msg.wt)

	case searchMsg:
		m.showSearch(msg)
		return m, nil

	case diffMsg:
		m.add(&block{kind: kindCmdOut, text: string(msg)})
		m.refresh()
		return m, nil

	case updateMsg:
		return m, m.handleUpdate(msg)

	case updateDoneMsg:
		m.handleUpdateDone(msg)
		return m, nil

	case mcpApprovedMsg:
		return m, m.handleMCPApproved(msg)

	case clipImageMsg:
		if msg.err != nil {
			m.note(msg.err.Error(), false)
		} else {
			m.input.InsertString(m.attachImage(msg.img))
			m.afterInput()
		}
		return m, nil

	case loginResultMsg:
		if msg.claude {
			m.setKittyKeys(true) // the login process had the terminal
		}
		cmd := m.handleLogin(msg)
		m.layout()
		return m, cmd

	case eventMsg:
		if msg.gen != m.gen {
			if i, ok := m.sessionOf(msg.gen); ok {
				m.backgroundEvent(i, msg.ev)
				return m, listen(msg.gen, msg.ch)
			}
			return m, nil
		}
		m.handleEvent(msg.ev)
		m.layout()
		m.refresh()
		return m, listen(msg.gen, msg.ch)

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if seq, ok := csiBytes(msg); ok {
		if k, ok := csiKey(seq); ok {
			return m.handleKey(k)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	typing := !m.pop.open() && len(m.perms) == 0
	if k.Type == tea.KeyRunes && k.Paste && typing {
		// Before termSafe: the path must match the file name exactly.
		if refs, ok, err := m.pastedImages(string(k.Runes)); ok {
			if err != nil {
				m.note(err.Error(), false)
			} else {
				m.input.InsertString(refs)
				m.afterInput()
			}
			return m, nil
		}
	}
	if k.String() == "ctrl+v" && typing {
		return m, clipboardImage
	}
	if k.Type == tea.KeyRunes {
		k.Runes = []rune(termSafe(string(k.Runes)))
		if k.Paste && typing {
			if ref, ok := m.collapsePaste(string(k.Runes)); ok {
				m.input.InsertString(ref)
				m.afterInput()
				return m, nil
			}
		}
	}
	// A selection is dismissed by any key; ctrl+c copies it first, esc
	// just clears it.
	if m.sel.has {
		switch k.String() {
		case "ctrl+c":
			m.copySelection()
			m.clearSelection()
			return m, nil
		case "esc":
			m.clearSelection()
			return m, nil
		}
		m.clearSelection()
	}

	// Global keys work in every state.
	if ks := k.String(); len(ks) == 5 && strings.HasPrefix(ks, "alt+") && ks[4] >= '1' && ks[4] <= '9' {
		return m, m.switchTo(int(ks[4] - '1'))
	}
	switch k.String() {
	case "ctrl+c":
		if m.pop.open() {
			m.closePopup(true)
			return m, nil
		}
		if m.input.Value() != "" {
			m.input.Reset()
			m.afterInput()
			return m, nil
		}
		if time.Since(m.ctrlC) < time.Second {
			return m, m.quit()
		}
		m.ctrlC = time.Now()
		m.note("press ctrl+c again to quit", false)
		return m, nil
	case "pgup":
		m.vp.HalfViewUp()
		return m, nil
	case "pgdown":
		m.vp.HalfViewDown()
		return m, nil
	case "ctrl+o":
		return m, m.toggleDetails()
	case "ctrl+b":
		return m, m.toggleSidebar()
	case "ctrl+y":
		return m, m.copyLast()
	case "ctrl+k":
		if m.pop.mode == popPalette {
			m.closePopup(true)
		} else if len(m.perms) == 0 {
			m.openPalette()
		}
		return m, nil
	}

	if len(m.perms) > 0 {
		return m, m.handlePermissionKey(k)
	}
	if m.pop.open() {
		if handled, cmd := m.handlePopupKey(k); handled {
			return m, cmd
		}
	}

	if k.String() == "f1" || (k.String() == "?" && m.input.Value() == "") {
		m.openHelp()
		return m, nil
	}
	// On the welcome screen, 1-4 fill in a starter prompt to edit or send.
	if m.input.Value() == "" && len(m.blocks) == 0 && m.level() != "pro" && k.Type == tea.KeyRunes && len(k.Runes) == 1 {
		if n := int(k.Runes[0] - '1'); n >= 0 && n < len(starters) {
			m.input.SetValue(starters[n])
			m.afterInput()
			return m, nil
		}
	}

	switch k.String() {
	case "shift+tab":
		m.cycleMode()
		return m, nil
	case "esc":
		// One stray Esc shouldn't throw away a long task: the first press
		// warns, a second within 1.5s interrupts.
		if m.busy && m.client != nil {
			if time.Since(m.escAt) > 1500*time.Millisecond {
				m.escAt = time.Now()
				m.note("press esc again to interrupt", false)
				return m, nil
			}
			m.escAt = time.Time{}
			m.client.Interrupt()
			m.note("interrupting…", false)
		}
		return m, nil
	case "enter":
		// A trailing backslash continues the line, for terminals that
		// can't send shift+enter.
		if v := m.input.Value(); strings.HasSuffix(v, "\\") && m.input.Line() == m.input.LineCount()-1 {
			m.input.SetValue(strings.TrimSuffix(v, "\\") + "\n")
			m.afterInput()
			return m, nil
		}
		raw := strings.TrimSpace(m.input.Value())
		if raw == "" {
			return m, nil
		}
		text := strings.TrimSpace(m.expandPastes(raw))
		if text != raw {
			m.pasteShown = raw
		}
		m.outImages = m.imagesIn(raw)
		m.input.Reset()
		cmd := m.submit(text)
		m.pasteShown, m.outImages = "", nil
		m.afterInput()
		return m, cmd
	case "up":
		if m.pop.open() {
			m.pop.close()
		}
		if m.input.Line() == 0 && len(m.history) > 0 && (m.input.Value() == "" || m.histIdx >= 0) {
			if m.histIdx < 0 {
				m.draft, m.histIdx = m.input.Value(), len(m.history)
			}
			if m.histIdx > 0 {
				m.histIdx--
				m.input.SetValue(m.history[m.histIdx])
				m.afterInput()
			}
			return m, nil
		}
	case "down":
		if m.histIdx >= 0 && m.input.Line() == m.input.LineCount()-1 {
			m.histIdx++
			if m.histIdx >= len(m.history) {
				m.histIdx = -1
				m.input.SetValue(m.draft)
			} else {
				m.input.SetValue(m.history[m.histIdx])
			}
			m.afterInput()
			return m, nil
		}
	}

	if k.Type == tea.KeyRunes || k.Type == tea.KeyBackspace || k.Type == tea.KeySpace {
		m.histIdx = -1
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.afterInput()
	return m, cmd
}

// afterInput resizes the input box and opens/refreshes the "/" and "@"
// suggestions, which appear as you type rather than needing a shortcut.
func (m *Model) afterInput() {
	m.input.SetHeight(max(1, min(m.input.LineCount(), 10)))

	// While browsing history, a recalled "/command" must not open the popup,
	// or the next ↑ would move through suggestions instead of history.
	if m.histIdx < 0 && (m.pop.mode == popNone || m.pop.mode == popSlash || m.pop.mode == popMention) {
		v := m.input.Value()
		switch {
		case strings.HasPrefix(v, "/") && !strings.ContainsAny(v, " \n"):
			if m.pop.mode != popSlash {
				m.pop = popup{mode: popSlash, all: m.slashChoices()}
			}
			m.pop.query = strings.TrimPrefix(v, "/")
			m.pop.filter()
		case mentionQuery(v) != nil:
			if m.pop.mode != popMention {
				m.pop = popup{mode: popMention, title: "Files", all: m.fileChoices()}
			}
			m.pop.query = *mentionQuery(v)
			m.pop.filter()
		default:
			m.pop.close()
		}
	}
	m.layout()
}

// mentionQuery returns the text after a trailing "@" token, if any.
func mentionQuery(v string) *string {
	if v == "" || strings.HasSuffix(v, " ") || strings.HasSuffix(v, "\n") {
		return nil
	}
	fields := strings.Fields(v)
	last := fields[len(fields)-1]
	if !strings.HasPrefix(last, "@") {
		return nil
	}
	q := last[1:]
	return &q
}

func (m *Model) handlePopupKey(k tea.KeyMsg) (bool, tea.Cmd) {
	p := &m.pop
	if p.mode == popSlider {
		return true, m.handleSliderKey(k)
	}
	if p.mode == popInput {
		switch k.Type {
		case tea.KeyEsc:
			m.closePopup(true)
		case tea.KeyEnter:
			v, submit := p.query, p.submit
			m.closePopup(false)
			var cmd tea.Cmd
			if submit != nil {
				cmd = submit(m, v)
			}
			m.layout()
			return true, cmd
		case tea.KeyBackspace:
			if r := []rune(p.query); len(r) > 0 {
				p.query = string(r[:len(r)-1])
			}
		case tea.KeyCtrlU:
			p.query = ""
		case tea.KeyRunes, tea.KeySpace:
			// Pasted keys often carry a trailing newline or spaces.
			p.query += strings.TrimRight(string(k.Runes), "\r\n")
		}
		return true, nil
	}
	own := p.mode == popPalette || p.mode == popPicker // popup has its own search field
	switch k.String() {
	case "up", "ctrl+p":
		p.move(-1)
		m.previewSel()
		return true, nil
	case "down", "ctrl+n":
		p.move(1)
		m.previewSel()
		return true, nil
	case "esc":
		m.closePopup(true)
		return true, nil
	case "tab":
		c, ok := p.current()
		if !ok {
			return true, nil
		}
		switch p.mode {
		case popSlash:
			m.input.SetValue("/" + c.value + " ")
			m.afterInput()
		case popMention:
			m.insertMention(c.value)
		default:
			p.move(1)
			m.previewSel()
		}
		return true, nil
	case "shift+tab":
		if own {
			p.move(-1)
			m.previewSel()
			return true, nil
		}
	case "enter":
		c, ok := p.current()
		if !ok {
			return true, nil
		}
		if p.held(c) {
			m.note("please read the notice first", false)
			return true, nil
		}
		if p.mode == popMention {
			m.insertMention(c.value)
			return true, nil
		}
		mode := p.mode
		m.closePopup(false)
		if mode == popSlash || c.run == nil {
			m.input.Reset()
			m.afterInput()
		}
		var cmd tea.Cmd
		if c.run != nil {
			cmd = c.run(m)
		}
		m.layout()
		return true, cmd
	case "backspace":
		if own {
			if p.query != "" {
				r := []rune(p.query)
				p.query = string(r[:len(r)-1])
				p.filter()
				m.previewSel()
			}
			return true, nil
		}
	}
	if own && (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) {
		p.query += string(k.Runes)
		if k.Type == tea.KeySpace {
			p.query += " "
		}
		p.sel = 0
		p.filter()
		m.previewSel()
		return true, nil
	}
	return own, nil // pickers swallow everything else; "/" and "@" fall through to the input
}

func (m *Model) previewSel() {
	if c, ok := m.pop.current(); ok && m.pop.preview != nil {
		m.pop.preview(m, c)
	}
}

func (m *Model) closePopup(cancelled bool) {
	if cancelled && m.pop.cancel != nil {
		m.pop.cancel(m)
	}
	m.pop.close()
	m.layout()
}

func (m *Model) insertMention(path string) {
	v := m.input.Value()
	i := strings.LastIndex(v, "@")
	m.input.SetValue(v[:i] + "@" + path + " ")
	m.pop.close()
	m.afterInput()
}

func (m *Model) handlePermissionKey(k tea.KeyMsg) tea.Cmd {
	p := m.perms[0]
	switch k.String() {
	case "y", "enter":
		m.client.Allow(p, false)
	case "a":
		if !hasSuggestions(p) {
			return nil
		}
		m.client.Allow(p, true)
	case "n", "esc":
		m.client.Deny(p, "The user denied this tool use. Stop and wait for their instructions.")
	default:
		return nil
	}
	m.perms = m.perms[1:]
	m.layout()
	return nil
}

func hasSuggestions(p *claude.PermissionRequest) bool {
	s := strings.TrimSpace(string(p.Suggestions))
	return s != "" && s != "null" && s != "[]"
}

// submit handles local slash commands and otherwise sends a user turn.
func (m *Model) submit(text string) tea.Cmd {
	m.remember(text)
	if strings.HasPrefix(text, "!") {
		return m.runShell(strings.TrimPrefix(text, "!"))
	}
	if strings.HasPrefix(text, "/") {
		name, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		if c, ok := m.localCommand(name, strings.TrimSpace(arg)); ok {
			return c
		}
		// A CLI command with enumerated arguments but none given: offer a
		// picker instead of letting the CLI print its usage text.
		if strings.TrimSpace(arg) == "" {
			for _, c := range m.commands {
				if c.Name == name {
					if opts := parseEnum(c.ArgumentHint); opts != nil {
						m.openArgPicker(c, opts)
						return nil
					}
				}
			}
		}
	}
	return m.send(text)
}

func (m *Model) send(text string) tea.Cmd {
	var cmd tea.Cmd
	if m.client == nil {
		// The process died (or never started); resume the same session.
		opts := m.cfg.Claude
		opts.Resume, opts.Continue = m.sessionID, false
		if m.engine == "" {
			m.input.SetValue(text) // keep the message for after connecting
			m.note("connect an AI first", false)
			return m.openLogin("")
		}
		if m.engine == "claude" && !m.claudeConfirmed() {
			m.input.SetValue(text) // keep the message for after the question
			m.gateClaude(opts)
			return nil
		}
		cmd = m.start(opts)
		if m.client == nil {
			return cmd
		}
	}
	var err error
	if imgs := m.outImages; len(imgs) > 0 {
		m.outImages = nil
		err = m.client.SendImages(text, imgs)
	} else {
		err = m.client.Send(text)
	}
	if err != nil {
		m.add(&block{kind: kindError, text: err.Error()})
		return cmd
	}
	if !m.busy {
		m.busy, m.turnStart, m.phase, m.turnFrom = true, time.Now(), "Thinking", len(m.blocks)
	}
	shown := text
	if m.pasteShown != "" {
		shown, m.pasteShown = m.pasteShown, ""
	}
	m.add(&block{kind: kindUser, text: shown})
	m.vp.GotoBottom()
	return cmd
}

func (m *Model) remember(text string) {
	m.histIdx = -1
	if n := len(m.history); n > 0 && m.history[n-1] == text {
		return
	}
	m.history = append(m.history, text)
	saveHistory(m.history)
}

func (m *Model) quit() tea.Cmd {
	m.closeAllSessions()
	m.setKittyKeys(false)
	return tea.Quit
}

func (m *Model) resetConversation() {
	m.blocks, m.tools, m.stream, m.todos, m.tasks = nil, map[string]*block{}, nil, nil, nil
	m.busy, m.perms, m.sessionID, m.phase, m.turnFrom = false, nil, "", "", 0
	m.cost, m.prevCost, m.context, m.warnedCtx = 0, 0, 0, false
	m.tokIn, m.tokOut, m.promptedModel, m.noProvider = 0, 0, false, nil
}

func (m *Model) clearConversation() tea.Cmd {
	if m.client != nil {
		m.client.Close()
	}
	m.resetConversation()
	opts := m.cfg.Claude
	opts.Resume, opts.Continue = "", false
	cmd := m.start(opts)
	m.refresh()
	m.note("new conversation", true)
	return cmd
}

func (m *Model) setModel(name string) {
	m.model = name
	if m.engine == "api" {
		recent := []string{name}
		for _, r := range m.settings.RecentModels {
			if r != name && len(recent) < 5 {
				recent = append(recent, r)
			}
		}
		m.settings.RecentModels = recent
		m.settings.APIModel = name
		saveSettings(m.settings)
	}
	if m.client != nil {
		m.client.SetModel(name)
	}
	m.note("model → "+name, true)
}

func (m *Model) setMode(mode string) {
	m.mode = mode
	if m.client != nil {
		m.client.SetPermissionMode(mode)
	}
	m.note(modeLabel(mode)+" — "+modeHelp(mode, m.engine), true)
	m.layout()
}

func (m *Model) cycleMode() {
	next := modes[0]
	for i, md := range modes {
		if md == m.mode {
			next = modes[(i+1)%len(modes)]
		}
	}
	m.setMode(next)
}

func (m *Model) setTheme(t Theme) {
	applyTheme(t)
	styleInput(&m.input)
	m.r.md = nil
	for _, b := range m.blocks {
		b.invalidate()
	}
	m.refresh()
}

func (m *Model) toggleDetails() tea.Cmd {
	m.r.expand = !m.r.expand
	m.settings.ToolDetail = m.r.expand
	saveSettings(m.settings)
	for _, b := range m.blocks {
		b.invalidate()
	}
	m.refresh()
	if m.r.expand {
		m.note("tool details shown", true)
	} else {
		m.note("tool details collapsed (click a tool to open it)", true)
	}
	return nil
}

func (m *Model) toggleSidebar() tea.Cmd {
	m.settings.HideSide = !m.settings.HideSide
	saveSettings(m.settings)
	m.layout()
	for _, b := range m.blocks {
		b.invalidate()
	}
	m.refresh()
	return nil
}

func (m *Model) toggleMouse() tea.Cmd {
	m.settings.NoMouse = !m.settings.NoMouse
	saveSettings(m.settings)
	if m.settings.NoMouse {
		m.note("mouse off: select text freely", true)
		return tea.DisableMouse
	}
	m.note("mouse on: wheel scrolls, click opens tools", true)
	return tea.EnableMouseCellMotion
}

func (m *Model) copyLast() tea.Cmd {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if b := m.blocks[i]; b.kind == kindAssistant && b.text != "" {
			copyToClipboard(b.text)
			m.note("copied last response", true)
			return nil
		}
	}
	m.note("nothing to copy yet", false)
	return nil
}

func (m *Model) handleEvent(ev claude.Event) {
	switch e := ev.(type) {
	case claude.Ready:
		m.commands, m.models = e.Commands, e.Models
		if e.PermissionMode != "" {
			m.mode = e.PermissionMode
		}
		if m.noProvider != nil && len(e.Models) > 0 {
			m.removeBlock(m.noProvider) // the hint has done its job
			m.noProvider = nil
		}
		if m.engine == "api" && !m.promptedModel && len(m.perms) == 0 {
			m.promptedModel = true
			switch {
			case len(e.Models) == 0:
				m.noProvider = &block{kind: kindInfo, text: "No API provider connected. For Claude with your Pro/Max subscription run /engine claude. For other models run /login (OpenRouter browser login, or an OpenAI / Gemini / Anthropic API key)."}
				m.add(m.noProvider)
			case m.model == "" && !m.pop.open():
				m.openModelPicker()
				m.note(fmt.Sprintf("%d models available: pick one", len(e.Models)), true)
			}
		}

	case claude.Init:
		m.sessionID = e.SessionID
		if e.Model != "" {
			m.model = e.Model // an engine without a model yet mustn't clear the user's pick
		}
		if e.Cwd != "" {
			m.cwd, m.r.cwd = e.Cwd, e.Cwd
		}
		if e.PermissionMode != "" {
			m.mode = e.PermissionMode
		}

	case claude.Status:
		if e.PermissionMode != "" {
			m.mode = e.PermissionMode
		}
		switch {
		case e.Status == "compacting":
			m.phase = "Compacting the conversation"
		case strings.HasPrefix(e.Status, "retrying"):
			m.phase = "Provider busy, r" + strings.TrimPrefix(e.Status, "r")
		}

	case claude.Compacted:
		text := "Conversation compacted into a summary to free context."
		if e.Auto {
			text = "The conversation was nearly too long for the model, so it was compacted into a summary."
		}
		if e.PreTokens > 0 {
			text += fmt.Sprintf(" (was %dk tokens)", e.PreTokens/1000)
		}
		m.add(&block{kind: kindInfo, text: text})
		m.context, m.warnedCtx = 0, false

	case claude.BlockStart:
		switch e.Kind {
		case "thinking":
			m.phase = "Thinking"
		case "text":
			m.phase = "Writing"
			m.stream = &block{kind: kindAssistant, streaming: true}
			m.add(m.stream)
		case "tool_use":
			m.phase = "Preparing " + displayName(e.ToolName)
		}

	case claude.TextDelta:
		if m.stream == nil {
			m.stream = &block{kind: kindAssistant, streaming: true}
			m.add(m.stream)
		}
		m.stream.text += e.Text
		m.stream.invalidate()

	case claude.AssistantMessage:
		if e.Parent == "" && e.Usage != nil && e.Usage.Context() > 0 {
			m.context = e.Usage.Context()
			if m.context > contextWarn && !m.warnedCtx {
				m.warnedCtx = true
				m.add(&block{kind: kindInfo, text: fmt.Sprintf(
					"Context is %dk tokens and every message resends it. /compact (or /clear for a new topic) keeps usage down.",
					m.context/1000)})
			}
		}
		for _, cb := range e.Blocks {
			switch cb.Type {
			case "text":
				if e.Parent != "" {
					continue
				}
				if m.stream != nil {
					m.stream.text, m.stream.streaming = cb.Text, false
					m.stream.invalidate()
					m.stream = nil
				} else if strings.TrimSpace(cb.Text) != "" {
					m.add(&block{kind: kindAssistant, text: cb.Text})
				}
			case "tool_use":
				if _, dup := m.tools[cb.ID]; dup {
					continue
				}
				var in map[string]any
				json.Unmarshal(cb.Input, &in)
				b := &block{kind: kindTool, id: cb.ID, name: cb.Name, input: in, nested: e.Parent != "", started: time.Now()}
				m.tools[cb.ID] = b
				m.add(b)
				switch cb.Name {
				case "TodoWrite":
					m.todos = in
				case "TaskUpdate":
					id, _ := in["taskId"].(string)
					st, _ := in["status"].(string)
					for i := range m.tasks {
						if m.tasks[i].id == id && st != "" {
							m.tasks[i].status = st
						}
					}
				}
				m.phase = "Running " + displayName(cb.Name)
			}
		}

	case claude.ToolResults:
		for _, r := range e.Results {
			b := m.tools[r.ToolUseID]
			if b == nil {
				continue
			}
			b.result = r.ResultText()
			b.state = toolDone
			if r.IsError {
				b.state = toolFailed
			}
			b.finished = time.Now()
			b.invalidate()
			if b.name == "TaskCreate" && b.state == toolDone {
				if mt := taskCreated.FindStringSubmatch(b.result); mt != nil {
					subject, _ := b.input["subject"].(string)
					m.tasks = append(m.tasks, task{id: mt[1], subject: subject, status: "pending"})
				}
			}
		}
		if e.Parent == "" {
			m.phase = "Thinking"
		}

	case *claude.PermissionRequest:
		m.perms = append(m.perms, e)
		m.pop.close()
		notify(m.notifyText(m.agentName() + " needs your approval: " + e.ToolName))

	case claude.RateLimit:
		if e.FiveHour != nil {
			m.fiveHour = e.FiveHour
		}
		if e.SevenDay != nil {
			m.sevenDay = e.SevenDay
		}

	case claude.Result:
		elapsed := time.Since(m.turnStart)
		m.finishTurn()
		m.cost = e.CostUSD
		if e.IsError || e.Subtype != "success" {
			msg := e.Text
			if msg == "" {
				msg = strings.ReplaceAll(e.Subtype, "_", " ")
			}
			if strings.Contains(msg, "during execution") {
				m.add(&block{kind: kindInfo, text: "Interrupted. Tell Claude what to do instead."})
			} else {
				m.add(&block{kind: kindError, text: msg})
			}
		}
		if e.NumTurns == 0 {
			// A local slash command: show its output as quiet command output
			// rather than as a Claude reply, and skip the cost footer.
			for _, b := range m.blocks[min(m.turnFrom, len(m.blocks)):] {
				if b.kind == kindAssistant {
					b.kind = kindCmdOut
					b.invalidate()
				}
			}
			return
		}
		in := e.Usage.InputTokens + e.Usage.CacheReadInputTokens + e.Usage.CacheCreationInputTokens
		m.tokIn, m.tokOut = m.tokIn+in, m.tokOut+e.Usage.OutputTokens
		spent := fmt.Sprintf("$%.3f", e.CostUSD-m.prevCost)
		if m.engine == "api" && e.CostUSD-m.prevCost == 0 {
			// Most providers don't report cost; tokens are the honest measure.
			spent = fmtTokens(in) + " in · " + fmtTokens(e.Usage.OutputTokens) + " out"
		}
		m.add(&block{kind: kindTurnEnd, text: fmt.Sprintf("Worked for %s · %s",
			fmtDur(time.Duration(e.DurationMS)*time.Millisecond), spent)})
		m.prevCost = e.CostUSD
		if elapsed > 20*time.Second || m.bg {
			// A session out of sight is worth a ping however short its turn was.
			notify(m.notifyText(m.agentName() + " finished"))
		}

	case claude.ControlResult:
		if e.Error != "" {
			m.note(e.Tag+": "+e.Error, false)
			return
		}
		if e.Tag == "mode" {
			var body struct{ Mode string }
			if json.Unmarshal(e.Body, &body) == nil && body.Mode != "" {
				m.mode = body.Mode
			}
		}

	case claude.Exited:
		m.finishTurn()
		m.client = nil
		if !e.Requested {
			msg := "claude exited"
			if e.Err != nil {
				msg += ": " + e.Err.Error()
			}
			if e.Stderr != "" {
				msg += "\n" + e.Stderr
			}
			m.add(&block{kind: kindError, text: msg + "\n(send a message to resume the session)"})
		}
	}
}

func (m *Model) finishTurn() {
	m.busy, m.perms, m.phase = false, nil, ""
	if m.stream != nil {
		m.stream.streaming = false
		m.stream.invalidate()
		m.stream = nil
	}
	for _, b := range m.tools {
		if b.state == toolRunning {
			b.state, b.finished = toolFailed, time.Now()
			if b.result == "" {
				b.result = "cancelled"
			}
			b.invalidate()
		}
	}
}

func (m *Model) removeBlock(target *block) {
	for i, b := range m.blocks {
		if b == target {
			m.blocks = append(m.blocks[:i], m.blocks[i+1:]...)
			return
		}
	}
}

func (m *Model) add(b *block) {
	b.dirty = true
	m.blocks = append(m.blocks, b)
}

// note shows a transient message in the status bar; ok=true is a
// confirmation, false a warning.
func (m *Model) note(s string, ok bool) {
	m.notice, m.noticeOK, m.noticeAt = s, ok, time.Now()
}

var taskCreated = regexp.MustCompile(`Task #(\w+) created`)

func fmtTokens(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
