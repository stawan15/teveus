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
	"github.com/stawan15/teveus/internal/antigravity"
	"github.com/stawan15/teveus/internal/claude"
)

// AgyOptions is where the Antigravity CLI lives; the zero value means "agy"
// on PATH.
type AgyOptions struct {
	Binary string
}

type Config struct {
	Claude    claude.Options
	Agy       AgyOptions
	Dark      bool
	Theme     string // overrides the saved theme when set
	Settings  Settings
	Full      bool   // disable the usage savers for this run
	Engine    string // overrides the saved engine when set
	Onboard   bool   // show first-run setup (set by main when it hasn't run yet)
	Version   string
	KeyOut    io.Writer     // the terminal, for key-protocol escapes; nil in tests
	Headless  bool          // teveus -p: no UI to ask in
	AskTrust  bool          // this folder hasn't been trusted yet (set by main; trust.go)
	PhoneAddr string        // serve the phone page on this address; "" = no page
	Send      func(tea.Msg) // hands a message to the running program (the phone page)
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
	mcpBlock   *block // the /mcp summary, replaced on each run so it doesn't pile up
	settings   Settings
	client     claude.Backend
	phone      *phoneServer
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
	tc           prefixCache
	sel          selection

	// clampFrame fits each row to the window; most rows repeat from frame to
	// frame, so the fitted form is cached. rowCacheW is the width it was
	// filled for and rowPad a row of blanks at that width.
	clamped   []string
	rowCache  map[string]string
	rowCacheW int
	rowPad    string

	// sideCache keeps the drawn sidebar: it changes only when one of the
	// values in sideKey does, and the spinner redraws the frame without it.
	sideCache   string
	sideLines   []string
	sideKeyUsed sideKey

	// headerCache keeps the drawn top bar so View() doesn't re-run regex/home
	// lookup on every frame.
	headerCache   string
	headerKeyUsed headerKey

	// inputCache keeps the drawn input box. A scroll or a streamed chunk
	// redraws the frame with the same box, and drawing it costs about a
	// seventh of a frame: the textarea's view goes through lipgloss.
	inputCache string
	inputKey   string

	blocks   []*block
	tools    map[string]*block
	stream   *block
	todos    map[string]any
	tasks    []task
	turnFrom int // index of the first block of the running turn

	busy      bool
	queue     []string // messages typed during a turn, sent one per finished turn
	phase     string
	turnStart time.Time
	perms     []*claude.PermissionRequest

	sessionID  string
	handoff    string // conversation summary carried over when switching engines
	model      string
	mode       string
	modeBefore string // what to fall back to when the CLI refuses a mode
	cwd        string
	branch     string
	cost       float64
	prevCost   float64
	context    int
	contextMax int
	planTier   string
	fiveHour   *claude.Window
	sevenDay   *claude.Window
	monthly    *claude.Window
	commands   []claude.Command
	models     []claude.ModelInfo
	files      []string
	project    projectInfo
	starters   []string

	pop             popup
	warnedCtx       bool
	promptedModel   bool
	claudeAuth      claudeAuthMsg
	authChecked     bool // the Claude Code login check has answered
	noProvider      *block
	tokIn, tokOut   int
	tokCache        int // cumulative cache-read tokens across the session
	turnIn, turnOut int // live tokens burned so far in the running turn
	turnCache       int
	turnTools       int
	lastTurnIn      int // tokens and cost of the most recently finished turn
	lastTurnOut     int
	lastTurnCache   int
	lastTurnCost    float64
	subagents       []*subagentState
	touchedFiles    []touchedFile
	history         []string
	histIdx         int
	draft           string
	ctrlC           time.Time
	escAt           time.Time
	interrupting    bool
	notice          string
	noticeOK        bool
	noticeAt        time.Time
	updated         string // a version just installed; lets the caller restart cleanly
	commitArg       string // the /commit argument, carried to the message it sends
	sendShown       string // what to put in the transcript instead of the text sent
	permFullDiff    bool   // user pressed 'd' to expand the full diff inside permView
	skillCount      int    // local skills/commands detected in this project, cached for sidebar
	ruleCache       string // cached hairline divider between header and workspace
	ruleCacheW      int
	ruleCacheSide   int
	ruleCacheThm    string
}

type eventMsg struct {
	gen int
	ev  claude.Event
	ch  <-chan claude.Event
}

// subagentState tracks a delegated Task/Agent subagent so the Left Rail and
// status bar can show what it is running and how many tokens it has burned.
type subagentState struct {
	id      string
	desc    string
	step    string
	tools   int
	tokIn   int
	tokOut  int
	started time.Time
	done    bool
	failed  bool
	ended   time.Time
}

// touchedFile records a file created or edited during this session.
type touchedFile struct {
	path    string
	action  string // "M" (edited) or "+" (written)
	added   int
	removed int
}

type tickMsg struct{}

// repaintMsg asks for a full redraw after the screen was drawn over
// (returning from ctrl+z). tea.ClearScreen resets the renderer's cached state.
type repaintMsg struct{}

func New(cfg Config) *Model {
	tightenConfig()
	ta := textarea.New()
	ta.Placeholder = "Ask anything, @ files, / commands, ! shell…"
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
	case engine != "api" && engine != "claude" && engine != "antigravity":
		engine = ""
	case cfg.Engine != "":
	case engine == "claude" && !settings.ClaudeNotice:
		engine = ""
	case engine == "antigravity" && !settings.AgyNotice:
		engine = ""
	case engine == "api" && !hasAPICredentials():
		engine = ""
	}
	model := cfg.Claude.Model
	if engine == "api" && model == "" {
		model = settings.APIModel
	}
	if engine == "antigravity" && model == "" {
		model = settings.AgyModel
	}
	vp := scroller{MouseWheelDelta: 3}
	m := &Model{
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
	m.setProject(cwd)
	// Teach the redactor the keys this install holds, so they are masked even
	// when they have no recognisable shape (a self-hosted base URL's token, a
	// key copied out of auth.json).
	agent.LoadKnownSecrets(configDir())
	return m
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
	var start tea.Cmd
	if m.cfg.AskTrust && !m.cfg.Headless {
		m.askTrust()
	} else {
		start = m.start(m.cfg.Claude)
	}
	return tea.Batch(textarea.Blink, tick(), start, indexFiles(m.cwd), checkClaudeAuth(m.cfg.Claude.Binary), m.pruneSessions(), m.startupUpdateCheck(), m.startPhoneServer())
}

// startPhoneServer opens the phone page when it's turned on, and puts its link
// in the transcript so it can be copied to the phone.
func (m *Model) startPhoneServer() tea.Cmd {
	addr := m.cfg.PhoneAddr
	if addr == "" && m.settings.PhonePage {
		addr = phoneAddr
	}
	if addr == "" || m.cfg.Send == nil {
		return nil
	}
	url, err := m.startPhone(addr, m.cfg.Send)
	if err != nil {
		m.add(&block{kind: kindError, text: "Phone page couldn't start on " + addr + ": " + err.Error()})
		return nil
	}
	m.settings.PhonePage = true
	saveSettings(m.settings)
	note := "Phone: open this on a device that can reach this machine —\n" + url +
		"\nAnyone with this link can prompt and approve. Turn it off with /phone off."
	if phoneIsPublicAddr(addr) {
		note += "\n⚠ It is listening on every interface over plain HTTP, so anyone on this network can read the traffic." +
			" Prefer the default (127.0.0.1) with a tunnel, or a network you trust."
	}
	m.add(&block{kind: kindInfo, text: note})
	return nil
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func tick() tea.Cmd { return tickIn(90 * time.Millisecond) }

func tickIn(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
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
	if m.gateClaude(opts) || m.gateAgy(opts) {
		return nil
	}
	opts = m.startOptions(opts)
	var c claude.Backend
	var err error
	switch m.engine {
	case "api":
		// WebFetch's guard is process-wide: it covers the tool whether the
		// engine, a subagent or a resumed session calls it.
		agent.SetWebFetchLocal(m.settings.WebFetchLocal)
		c, err = agent.Start(agent.Options{
			Cwd: m.cwd, Model: opts.Model, Mode: opts.PermissionMode,
			Store: agent.NewStore(configDir()), Style: opts.AppendPrompt,
			SessionDir: sessionDir(), Resume: opts.Resume, ConfigDir: configDir(), Effort: opts.Effort, Attribution: m.settings.Attribution, SubagentModel: m.settings.SubagentModel, LeanTools: !m.cfg.Full && m.settings.LeanTools,
		})
	case "antigravity":
		// The user's own Antigravity CLI, run as they installed it. It signs
		// in with their Google account; teveus never sees that login.
		c, err = antigravity.Start(antigravity.Options{
			Binary: m.cfg.Agy.Binary, Cwd: m.cwd, Model: opts.Model,
			PermissionMode: opts.PermissionMode, Resume: opts.Resume, Effort: opts.Effort,
			SessionDir: sessionDir(),
		})
	default:
		c, err = claude.Start(opts)
	}
	if err != nil {
		m.add(&block{kind: kindError, text: err.Error()})
		m.refresh()
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

	case tea.ResumeMsg:
		// Back from a stop: the shell drew over the screen while teveus was
		// stopped, and Bubble Tea's renderer still believes its last frame is
		// on screen, so nothing would be drawn. Ask for a clear, which also
		// resets that belief, and put the key protocol back.
		m.setKittyKeys(true)
		for _, b := range m.blocks {
			b.invalidate()
		}
		m.layout()
		m.refresh()
		// ReleaseTerminal turns mouse capture off, and RestoreTerminal doesn't
		// turn it back on, so without this the wheel stops scrolling after the
		// first stop until the app is restarted.
		if !m.settings.NoMouse {
			return m, tea.Batch(tea.EnableMouseCellMotion, func() tea.Msg { return repaintMsg{} })
		}
		return m, func() tea.Msg { return repaintMsg{} }

	case repaintMsg:
		return m, tea.ClearScreen

	case tickMsg:
		m.autoScroll()
		m.frame++
		m.r.spin = spinFrames[m.frame%len(spinFrames)]
		if m.settings.ReduceMotion {
			m.r.spin = "●"
		}
		if m.busy {
			anyRunning := false
			for _, b := range m.tools {
				if b.state == toolRunning {
					b.invalidate()
					anyRunning = true
				}
			}
			if anyRunning {
				m.refresh()
			} else if m.phone != nil {
				m.phone.update(m)
			}
		} else if m.phone != nil {
			// A page open on the phone reads what the last refresh copied, so
			// an idle window has to keep that copy current too.
			m.phone.update(m)
		}
		// Nothing animates while idle, so wake up less often (saves CPU on slow machines).
		if !m.busy && !m.sel.dragging {
			return m, tickIn(500 * time.Millisecond)
		}
		return m, tick()

	case filesMsg:
		m.files = msg
		return m, nil

	case claudeAuthMsg:
		m.claudeAuth, m.authChecked = msg, true
		// Setup waits for the login check so it can say whether Claude is connected.
		m.startSetup()
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

	case commitStartMsg:
		return m, m.startCommit()

	case updateMsg:
		return m, m.handleUpdate(msg)

	case updateDoneMsg:
		return m, m.handleUpdateDone(msg)

	case mcpApprovedMsg:
		return m, m.handleMCPApproved(msg)

	case mcpDoneMsg:
		return m, m.handleMCPDone(msg)

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
		// Drain any already-buffered events (e.g. bursts of TextDelta tokens)
		// before rebuilding layout and transcript once for the whole batch.
		closed := false
		if msg.ch != nil {
		drain:
			for i := 0; i < 64; i++ {
				select {
				case ev, ok := <-msg.ch:
					if !ok {
						closed = true
						break drain
					}
					m.handleEvent(ev)
				default:
					break drain
				}
			}
		}
		m.layout()
		m.refresh()
		if closed {
			return m, nil
		}
		return m, listen(msg.gen, msg.ch)

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case phoneMsg:
		return m, m.handlePhone(msg)

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
	// Cmd+V with only a picture on the clipboard pastes nothing, so an empty
	// paste is a request for the image too.
	if typing && (k.String() == "ctrl+v" || k.Type == tea.KeyRunes && k.Paste && len(k.Runes) == 0) {
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
	case "ctrl+z":
		// Suspend the way Bubble Tea restores the terminal first. Left to the
		// terminal, ctrl+z stops the process while the alternate screen, the
		// key protocol and mouse capture are still on, so (fg) comes back
		// printing into a screen that was never restored.
		return m, func() tea.Msg { return tea.Suspend() }
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
	case "shift+up":
		m.vp.ScrollUp(m.vp.MouseWheelDelta)
		return m, nil
	case "shift+down":
		m.vp.ScrollDown(m.vp.MouseWheelDelta)
		return m, nil
	case "ctrl+e":
		if len(m.queue) > 0 {
			m.editQueued(len(m.queue) - 1)
			return m, nil
		}
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
	if prompts := m.starterList(); m.input.Value() == "" && len(m.blocks) == 0 && m.level() != "pro" && k.Type == tea.KeyRunes && len(k.Runes) == 1 {
		if n := int(k.Runes[0] - '1'); n >= 0 && n < len(prompts) {
			m.input.SetValue(prompts[n])
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
		// warns, a second within 3s interrupts, and a third while still
		// interrupting force-cancels so the UI never stays stuck.
		if m.busy && m.client != nil {
			if m.interrupting {
				m.client.Interrupt()
				m.finishTurn()
				m.add(&block{kind: kindInfo, text: "Interrupted."})
				m.note("force interrupted", false)
				m.refresh()
				return m, nil
			}
			if time.Since(m.escAt) > 3*time.Second {
				m.escAt = time.Now()
				m.note("press esc again to interrupt", false)
				return m, nil
			}
			m.escAt = time.Time{}
			m.interrupting = true
			m.client.Interrupt()
			m.note("interrupting… (press esc again to force)", false)
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
	if p.mode == popReview {
		switch k.String() {
		case "up", "ctrl+p":
			p.move(-1)
			m.previewSel()
		case "down", "ctrl+n":
			p.move(1)
			m.previewSel()
		case "esc":
			back := p.back
			p.close()
			m.layout()
			if back != nil {
				return true, back(m)
			}
		case "enter":
			c, ok := p.current()
			if !ok {
				return true, nil
			}
			run := c.run
			p.close()
			m.layout()
			if run != nil {
				return true, run(m)
			}
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

// handlePhone applies what the phone page asked for, through the same paths
// the keyboard uses: a permission answer goes to the prompt it answers, and a
// message goes through submit().
func (m *Model) handlePhone(msg phoneMsg) tea.Cmd {
	switch {
	case msg.interrupt:
		if m.busy && m.client != nil {
			m.client.Interrupt()
			m.note("interrupting…", false)
		}
	case msg.mode != "":
		m.setMode(msg.mode)
		m.refresh()
	case msg.session != nil:
		cmd := m.switchTo(*msg.session)
		m.refresh()
		return cmd
	case msg.popup:
		cmd := m.answerPhonePopup(msg.index, msg.text)
		m.refresh()
		return cmd
	case msg.perm != "":
		if m.client == nil {
			m.note("no session is running", false)
			return nil
		}
		// The prompt may have been answered in the terminal already.
		if len(m.perms) == 0 || m.perms[0].RequestID != msg.request {
			m.note("that approval was already answered", false)
			return nil
		}
		p := m.perms[0]
		switch msg.perm {
		case "allow":
			m.client.Allow(p, false)
		case "always":
			m.client.Allow(p, true)
		default:
			m.client.Deny(p, "The user denied this tool use. Stop and wait for their instructions.")
		}
		m.perms = m.perms[1:]
		m.layout()
		m.refresh()
	case msg.text != "":
		cmd := m.submit(msg.text)
		m.refresh()
		return cmd
	}
	return nil
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
	case "d":
		m.permFullDiff = !m.permFullDiff
		m.layout()
		return nil
	default:
		return nil
	}
	m.permFullDiff = false
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
			for _, c := range m.allCommands() {
				if c.Name == name {
					if opts := parseEnum(c.ArgumentHint); opts != nil {
						m.openArgPicker(c, opts)
						return nil
					}
				}
			}
		}
		// Claude Code reads its own ".claude" files, not teveus's own layouts,
		// so those commands are filled in here.
		if m.engine != "api" {
			if expanded, ok := agent.ExpandOwn(m.cwd, configDir(), text); ok {
				m.sendShown = strings.TrimSpace(text)
				text = expanded
			}
		}
	}
	return m.send(text)
}

func (m *Model) send(text string) tea.Cmd {
	if m.busy && m.client != nil && len(m.outImages) == 0 {
		// A backend that runs its own loop can take the message into the turn
		// already running, and the model reads it between tool calls. Claude
		// Code can't, so there the message waits for the turn to finish.
		if inj, ok := m.client.(claude.Injector); ok && inj.Inject(text) == nil {
			return nil
		}
		m.queue, m.pasteShown = append(m.queue, text), ""
		return nil
	}
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
		if m.engine == "antigravity" && !m.agyConfirmed() {
			m.input.SetValue(text) // keep the message for after the question
			m.gateAgy(opts)
			return nil
		}
		cmd = m.start(opts)
		if m.client == nil {
			return cmd
		}
	}
	wireText := text
	if m.handoff != "" {
		wireText = "<conversation_history>\n" + m.handoff + "\n</conversation_history>\n\n" + text
	}
	var err error
	if imgs := m.outImages; len(imgs) > 0 {
		m.outImages = nil
		err = m.client.SendImages(wireText, imgs)
	} else {
		err = m.client.Send(wireText)
	}
	if err != nil {
		m.add(&block{kind: kindError, text: err.Error()})
		m.refresh()
		return cmd
	}
	m.handoff = ""
	if !m.busy {
		m.busy, m.turnStart, m.phase, m.turnFrom = true, time.Now(), "Thinking", len(m.blocks)
		m.turnIn, m.turnOut, m.turnCache, m.turnTools = 0, 0, 0, 0
	}
	shown := text
	if m.pasteShown != "" {
		shown, m.pasteShown = m.pasteShown, ""
	}
	if m.sendShown != "" {
		shown, m.sendShown = m.sendShown, ""
	}
	m.add(&block{kind: kindUser, text: shown})
	m.refresh()
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
	m.stopPhone()
	m.setKittyKeys(false)
	return tea.Quit
}

func (m *Model) resetConversation() {
	m.blocks, m.tools, m.stream, m.todos, m.tasks, m.queue = nil, map[string]*block{}, nil, nil, nil, nil
	m.busy, m.perms, m.sessionID, m.handoff, m.phase, m.turnFrom = false, nil, "", "", "", 0
	m.cost, m.prevCost, m.context, m.contextMax, m.planTier, m.warnedCtx = 0, 0, 0, 0, "", false
	m.tokIn, m.tokOut, m.tokCache, m.promptedModel, m.noProvider = 0, 0, 0, false, nil
	m.turnIn, m.turnOut, m.turnCache, m.turnTools = 0, 0, 0, 0
	m.lastTurnIn, m.lastTurnOut, m.lastTurnCache, m.lastTurnCost = 0, 0, 0, 0
	m.subagents, m.touchedFiles = nil, nil
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
	} else if m.engine == "antigravity" {
		m.settings.AgyModel = name
		saveSettings(m.settings)
	}
	if m.client != nil {
		m.client.SetModel(name)
	}
	if m.engine == "api" {
		// Another provider may have no allowance to report; the engine sends
		// new windows when it has them.
		m.fiveHour, m.sevenDay, m.monthly = nil, nil, nil
	}
	m.note("model → "+name, true)
}

func (m *Model) setMode(mode string) {
	m.modeBefore = m.mode
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
	m.sideCache, m.inputCache, m.ruleCache = "", "", ""
	for k := range m.rowCache {
		delete(m.rowCache, k)
	}
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
		if e.Cwd != "" && e.Cwd != m.cwd {
			m.cwd, m.r.cwd = e.Cwd, e.Cwd
			m.setProject(e.Cwd)
		}
		if e.PermissionMode != "" {
			m.mode = e.PermissionMode
		}

	case claude.Notice:
		m.add(&block{kind: kindInfo, text: e.Text})
		if !m.bg {
			m.vp.GotoBottom()
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

	case claude.UserMessage:
		// Said while the turn was running: show it where it landed.
		m.add(&block{kind: kindUser, text: e.Text})
		if !m.bg {
			m.vp.GotoBottom()
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
		if e.Usage != nil {
			stepIn := e.Usage.Context()
			stepOut := e.Usage.OutputTokens
			stepCache := e.Usage.CacheReadInputTokens
			m.turnIn += stepIn
			m.turnOut += stepOut
			m.turnCache += stepCache
			if e.Parent == "" && stepIn > 0 {
				m.context = stepIn
				if m.context > contextWarn && !m.warnedCtx {
					m.warnedCtx = true
					m.add(&block{kind: kindInfo, text: fmt.Sprintf(
						"Context is %dk tokens and every message resends it. /compact (or /clear for a new topic) keeps usage down.",
						m.context/1000)})
				}
			} else if e.Parent != "" {
				if sub := m.findSubagent(e.Parent); sub != nil {
					sub.tokIn += stepIn
					sub.tokOut += stepOut
				}
				if pb := m.tools[e.Parent]; pb != nil {
					pb.subTokIn += stepIn
					pb.subTokOut += stepOut
					pb.invalidate()
				}
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
				m.turnTools++
				if e.Parent == "" && (cb.Name == "Task" || cb.Name == "Agent") {
					desc, _ := in["description"].(string)
					if desc == "" {
						desc = "Subagent"
					}
					m.subagents = append(m.subagents, &subagentState{
						id: cb.ID, desc: desc, step: "starting", started: b.started,
					})
				} else if e.Parent != "" {
					step := displayName(cb.Name)
					if sum := m.r.summary(cb.Name, in); sum != "" {
						step += " " + sum
					}
					if sub := m.findSubagent(e.Parent); sub != nil {
						sub.step = step
						sub.tools++
					}
					if pb := m.tools[e.Parent]; pb != nil {
						pb.subTools++
						pb.invalidate()
					}
				}
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

	case claude.ToolProgress:
		if b := m.tools[e.ToolUseID]; b != nil && b.state == toolRunning {
			b.result = e.Output
			b.invalidate()
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
			if b.name == "Task" || b.name == "Agent" {
				if sub := m.findSubagent(b.id); sub != nil {
					sub.done = true
					sub.failed = b.state == toolFailed
					sub.ended = b.finished
				}
			}
			if b.state == toolDone {
				switch b.name {
				case "Edit", "MultiEdit", "Write":
					m.recordTouchedFile(b)
				case "TaskCreate":
					if mt := taskCreated.FindStringSubmatch(b.result); mt != nil {
						subject, _ := b.input["subject"].(string)
						m.tasks = append(m.tasks, task{id: mt[1], subject: subject, status: "pending"})
					}
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
		if e.Monthly != nil {
			m.monthly = e.Monthly
		}

	case claude.ContextUsage:
		if e.Used > 0 {
			m.context = e.Used
		}
		if e.Size > 0 {
			m.contextMax = e.Size
		}
		if e.Tier != "" {
			m.planTier = e.Tier
		}

	case claude.Result:
		defer m.sendQueued()
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
		in := e.Usage.Context()
		out := e.Usage.OutputTokens
		cacheRead := e.Usage.CacheReadInputTokens
		if in == 0 && out == 0 {
			in, out, cacheRead = m.turnIn, m.turnOut, m.turnCache
		}
		m.tokIn, m.tokOut, m.tokCache = m.tokIn+in, m.tokOut+out, m.tokCache+cacheRead
		deltaCost := max(e.CostUSD-m.prevCost, 0)
		m.lastTurnIn, m.lastTurnOut, m.lastTurnCache, m.lastTurnCost = in, out, cacheRead, deltaCost

		// Quiet per-turn receipt: duration, token usage, cache hit %, cost and tools.
		parts := []string{"Worked for " + fmtDur(time.Duration(e.DurationMS)*time.Millisecond)}
		if in+out > 0 {
			parts = append(parts, fmtTokens(in)+" in · "+fmtTokens(out)+" out")
			if cacheRead > 0 && in > 0 {
				if pct := min(cacheRead*100/in, 100); pct > 0 {
					parts = append(parts, fmt.Sprintf("%d%% cached", pct))
				}
			}
		}
		if deltaCost > 0 || (m.engine != "api" && in+out == 0) {
			parts = append(parts, fmt.Sprintf("$%.3f", deltaCost))
		}
		if m.turnTools > 0 {
			parts = append(parts, plural(m.turnTools, "%d tool"))
		}
		m.add(&block{kind: kindTurnEnd, text: strings.Join(parts, " · ")})
		m.turnIn, m.turnOut, m.turnCache, m.turnTools = 0, 0, 0, 0
		m.prevCost = e.CostUSD
		if elapsed > 20*time.Second || m.bg {
			// A session out of sight is worth a ping however short its turn was.
			notify(m.notifyText(m.agentName() + " finished"))
		}

	case claude.ControlResult:
		if e.Error != "" {
			// A mode the CLI won't take (auto is not available on every model)
			// used to leave the pill reading "autopilot" while the session was
			// still asking: put the mode it kept back on screen.
			if e.Tag == "mode" && m.modeBefore != "" {
				m.mode, m.modeBefore = m.modeBefore, ""
				m.note("still "+modeLabel(m.mode)+": "+e.Error, false)
				m.layout()
				return
			}
			m.note(e.Tag+": "+e.Error, false)
			return
		}
		if e.Tag == "mode" {
			m.modeBefore = ""
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

// editQueued takes a waiting message back into the input to be changed.
func (m *Model) editQueued(i int) {
	if i < 0 || i >= len(m.queue) {
		return
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		m.note("finish or clear the current message first", false)
		return
	}
	m.input.SetValue(m.queue[i])
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
}

func (m *Model) sendQueued() {
	if len(m.queue) > 0 && !m.busy && m.client != nil {
		next := m.queue[0]
		m.queue = m.queue[1:]
		m.send(next)
	}
}

func (m *Model) finishTurn() {
	m.busy, m.perms, m.phase = false, nil, ""
	m.interrupting, m.escAt = false, time.Time{}
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
	for _, sub := range m.subagents {
		if !sub.done {
			sub.done, sub.failed, sub.ended = true, true, time.Now()
		}
	}
}

func (m *Model) findSubagent(id string) *subagentState {
	for _, s := range m.subagents {
		if s.id == id {
			return s
		}
	}
	return nil
}

func (m *Model) activeSubagents() int {
	n := 0
	for _, s := range m.subagents {
		if !s.done {
			n++
		}
	}
	return n
}

func (m *Model) recordTouchedFile(b *block) {
	p, _ := b.input["file_path"].(string)
	if p == "" {
		return
	}
	rel := m.r.rel(p)
	action := "M"
	added, removed := 0, 0
	switch b.name {
	case "Write":
		action = "+"
		content, _ := b.input["content"].(string)
		added = len(splitLines(content))
	case "Edit":
		old, _ := b.input["old_string"].(string)
		nw, _ := b.input["new_string"].(string)
		a, n := diffCounts(old, nw)
		added, removed = a, n
	case "MultiEdit":
		if edits, ok := b.input["edits"].([]any); ok {
			for _, e := range edits {
				em, _ := e.(map[string]any)
				old, _ := em["old_string"].(string)
				nw, _ := em["new_string"].(string)
				a, n := diffCounts(old, nw)
				added += a
				removed += n
			}
		}
	}
	for i := range m.touchedFiles {
		if m.touchedFiles[i].path == rel {
			m.touchedFiles[i].added += added
			m.touchedFiles[i].removed += removed
			// Move most recently touched file to front.
			tf := m.touchedFiles[i]
			copy(m.touchedFiles[1:i+1], m.touchedFiles[:i])
			m.touchedFiles[0] = tf
			return
		}
	}
	m.touchedFiles = append([]touchedFile{{path: rel, action: action, added: added, removed: removed}}, m.touchedFiles...)
}

func diffCounts(old, nw string) (added, removed int) {
	a, b := splitLines(old), splitLines(nw)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	return len(b) - pre - suf, len(a) - pre - suf
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
