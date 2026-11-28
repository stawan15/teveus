// Package antigravity drives Google's Antigravity CLI (`agy`) over its Agent
// Client Protocol (ACP) stdio server.
//
// The CLI is started as `agy acp`, which speaks JSON-RPC 2.0 on stdin/stdout:
//
//	initialize                        -> protocol version, capabilities
//	session/new or session/resume     -> session id, models & configOptions
//	session/set_config_option         -> live model & thinking-level switching
//	session/prompt                    -> the turn; the answer streams back as
//	                                     session/update notifications
//	session/request_permission        <- the CLI asks before a tool runs; we
//	                                     answer with the user's choice
//
// Everything it reports is decoded into the claude.Event types, so the UI does
// not care which engine is running.
//
// Naming: this is Antigravity's own program, run as the user installed it, with
// the user's own sign-in. teveus is not affiliated with Google and the engine
// is labelled "Google (Antigravity CLI)" to avoid implying endorsement.
package antigravity

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

type Options struct {
	Binary         string // defaults to "agy"
	Cwd            string
	Model          string
	PermissionMode string // "default", "acceptEdits" or "auto"
	Resume         string // conversation id to resume
	Effort         string // low, medium, high, xhigh, max
	AppendPrompt   string
	SessionDir     string // directory for saved teveus session transcripts
}

type configOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

type availableModel struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
}

// Client is one `agy acp` process.
type Client struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	events     chan claude.Event
	stderr     *tailBuffer
	cwd        string
	sessionDir string

	writeMu sync.Mutex
	nextID  atomic.Int64
	closed  atomic.Bool
	ready   chan struct{} // closed once handshake finishes (or fails)
	done    chan struct{} // closed when read() exits

	mu             sync.Mutex
	readyErr       error
	session        string
	model          string
	effort         string
	models         []claude.ModelInfo
	useConfigModel bool
	thoughtLevels  []string
	mode           string // permission mode, as the UI names it
	waiting        map[string]chan *rpcMessage

	// Conversation history saved to teveus's sessionDir for /resume and /search.
	history     []agent.Message
	turnText    strings.Builder
	turnTools   []agent.ToolCall
	turnToolIdx map[string]int
	turnResults []agent.Message
	turnResSeen map[string]bool

	// pending permissions by JSON-RPC id, so an answer reaches the right ask.
	perms  map[string]*claude.PermissionRequest
	always map[string]bool // tool names the user allowed for this session
}

// Start launches the CLI and completes the handshake, so the caller can send
// a turn as soon as it returns.
func Start(opts Options) (*Client, error) {
	bin := opts.Binary
	if bin == "" {
		bin = "agy"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%s not found in PATH (install it from Google, then try again)", bin)
	}
	cmd := exec.Command(path, "acp")
	// The user's own home and PATH: the CLI signs in with the account they
	// already use, and teveus never sees that login.
	cmd.Env = os.Environ()
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{max: 4000}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &Client{
		cmd: cmd, stdin: stdin, events: make(chan claude.Event, 256), stderr: stderr,
		cwd: opts.Cwd, sessionDir: opts.SessionDir, effort: opts.Effort,
		ready: make(chan struct{}), done: make(chan struct{}),
		perms: map[string]*claude.PermissionRequest{}, always: map[string]bool{},
		mode: opts.PermissionMode, waiting: map[string]chan *rpcMessage{},
	}
	go c.read(stdout)
	go c.boot(opts)
	return c, nil
}

// boot runs the ACP handshake in the background so Start returns immediately
// and the UI paints its first frame without waiting for the CLI to boot.
func (c *Client) boot(opts Options) {
	if err := c.handshake(opts); err != nil {
		c.mu.Lock()
		c.readyErr = err
		c.mu.Unlock()
		close(c.ready)
		c.emit(claude.Exited{Err: err, Stderr: strings.TrimSpace(c.stderr.String())})
		c.Close()
		return
	}
	close(c.ready)
}

func (c *Client) Events() <-chan claude.Event { return c.events }

// handshake initializes the protocol and opens (or resumes) a session.
func (c *Client) handshake(opts Options) error {
	if _, err := c.request("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			// teveus does not proxy file reads: the CLI has its own tools, and
			// answering these would mean editing files behind its back.
			"fs": map[string]any{"readTextFile": false, "writeTextFile": false},
		},
	}); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	var res json.RawMessage
	var err error
	if opts.Resume != "" {
		res, err = c.request("session/resume", map[string]any{
			"sessionId":  opts.Resume,
			"cwd":        opts.Cwd,
			"mcpServers": []any{},
		})
	}
	if opts.Resume == "" || err != nil {
		res, err = c.request("session/new", map[string]any{
			"cwd":        opts.Cwd,
			"mcpServers": []any{},
		})
	}
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}

	var out struct {
		SessionID string `json:"sessionId"`
		Models    *struct {
			AvailableModels []availableModel `json:"availableModels"`
			CurrentModelID  string           `json:"currentModelId"`
		} `json:"models"`
		ConfigOptions []configOption `json:"configOptions"`
		Meta          *struct {
			Antigravity *struct {
				Tier *struct {
					Name string `json:"name"`
				} `json:"tier"`
			} `json:"antigravity"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return err
	}
	if out.SessionID == "" {
		return errors.New("the CLI opened no session")
	}

	var avail []availableModel
	defaultModel := ""
	if out.Models != nil {
		avail = out.Models.AvailableModels
		defaultModel = out.Models.CurrentModelID
	}
	models, cfgModel, levels, hasCfgModel := parseSessionModels(out.ConfigOptions, avail)
	if cfgModel != "" {
		defaultModel = cfgModel
	}
	activeModel := opts.Model
	if activeModel == "" {
		activeModel = defaultModel
	}

	c.mu.Lock()
	c.session = out.SessionID
	c.model = activeModel
	c.models = models
	c.useConfigModel = hasCfgModel
	c.thoughtLevels = levels
	if opts.Resume != "" && c.sessionDir != "" {
		if prev, loadErr := agent.LoadSession(c.sessionDir, opts.Resume); loadErr == nil {
			c.history = prev.History
		}
	}
	modelsCopy := append([]claude.ModelInfo(nil), c.models...)
	c.mu.Unlock()

	// The session starts in the CLI's own default; apply the user's choice,
	// model, and effort before emitting Ready/Init so the UI reflects them.
	if opts.PermissionMode != "" {
		_ = c.applyPermissionMode(opts.PermissionMode)
	}
	if opts.Model != "" {
		_ = c.applyModel(opts.Model)
	}
	if opts.Effort != "" {
		_ = c.applyEffort(opts.Effort)
	}

	c.emit(claude.Ready{Models: modelsCopy, PermissionMode: permissionModeLabel(opts.PermissionMode)})
	c.emit(claude.Init{SessionID: out.SessionID, Model: activeModel, Cwd: opts.Cwd,
		PermissionMode: permissionModeLabel(opts.PermissionMode)})
	if out.Meta != nil && out.Meta.Antigravity != nil && out.Meta.Antigravity.Tier != nil && out.Meta.Antigravity.Tier.Name != "" {
		c.emit(claude.ContextUsage{Tier: out.Meta.Antigravity.Tier.Name})
	}
	return nil
}

// Send starts a turn.
func (c *Client) Send(text string) error { return c.SendImages(text, nil) }

func (c *Client) SendImages(text string, images []claude.Image) error {
	if c.closed.Load() {
		return errors.New("closed")
	}
	prompt := []map[string]any{{"type": "text", "text": text}}
	for _, im := range images {
		prompt = append(prompt, map[string]any{
			"type":     "image",
			"mimeType": im.MediaType,
			"data":     base64.StdEncoding.EncodeToString(im.Data),
		})
	}
	c.mu.Lock()
	c.history = append(c.history, agent.Message{Role: "user", Text: text})
	c.turnText.Reset()
	c.turnTools = nil
	c.turnToolIdx = map[string]int{}
	c.turnResults = nil
	c.turnResSeen = map[string]bool{}
	c.mu.Unlock()

	go func() {
		if c.ready != nil {
			<-c.ready
		}
		c.mu.Lock()
		initErr, sid := c.readyErr, c.session
		c.mu.Unlock()
		if initErr != nil {
			c.emit(claude.Result{Subtype: "error", IsError: true, Text: initErr.Error()})
			return
		}
		turnStart := time.Now()
		res, err := c.requestNoTimeout("session/prompt", map[string]any{"sessionId": sid, "prompt": prompt})
		c.finishTurnAndSave()
		if err != nil {
			c.emit(claude.Result{Subtype: "error", IsError: true, Text: err.Error(), NumTurns: 1,
				DurationMS: int(time.Since(turnStart).Milliseconds())})
			return
		}
		var out struct {
			StopReason string `json:"stopReason"`
			Usage      struct {
				InputTokens       int `json:"inputTokens"`
				OutputTokens      int `json:"outputTokens"`
				CachedReadTokens  int `json:"cachedReadTokens"`
				CachedWriteTokens int `json:"cachedWriteTokens"`
			} `json:"usage"`
		}
		json.Unmarshal(res, &out)
		c.emit(claude.Result{
			Subtype:    stopReasonSubtype(out.StopReason),
			NumTurns:   1,
			DurationMS: int(time.Since(turnStart).Milliseconds()),
			Usage: claude.Usage{
				InputTokens:              out.Usage.InputTokens,
				OutputTokens:             out.Usage.OutputTokens,
				CacheCreationInputTokens: out.Usage.CachedWriteTokens,
				CacheReadInputTokens:     out.Usage.CachedReadTokens,
			},
		})
	}()
	return nil
}

// finishTurnAndSave flushes the accumulated assistant text and tool calls from
// the current turn into history and persists the session to teveus's sessionDir.
func (c *Client) finishTurnAndSave() {
	c.mu.Lock()
	text := c.turnText.String()
	tools := c.turnTools
	results := c.turnResults
	c.turnText.Reset()
	c.turnTools = nil
	c.turnToolIdx = nil
	c.turnResults = nil
	c.turnResSeen = nil

	if strings.TrimSpace(text) != "" || len(tools) > 0 {
		c.history = append(c.history, agent.Message{
			Role:      "assistant",
			Text:      text,
			ToolCalls: tools,
		})
	}
	if len(results) > 0 {
		c.history = append(c.history, results...)
	}

	dir, sid, cwd, model := c.sessionDir, c.session, c.cwd, c.model
	hist := append([]agent.Message(nil), c.history...)
	c.mu.Unlock()

	if dir == "" || sid == "" || len(hist) == 0 {
		return
	}
	_ = agent.SaveSession(dir, agent.Session{
		ID:      sid,
		Engine:  "antigravity",
		Cwd:     cwd,
		Model:   model,
		Title:   agent.SessionTitle(hist),
		Updated: time.Now(),
		History: hist,
	})
}

// Interrupt cancels the turn that is running. Per ACP, session/cancel is a
// JSON-RPC notification (no id, no reply), so sending it must never block
// waiting for a response.
func (c *Client) Interrupt() error {
	c.mu.Lock()
	sid := c.session
	c.mu.Unlock()
	if sid == "" {
		return nil
	}
	return c.notify("session/cancel", map[string]any{"sessionId": sid})
}

// SetPermissionMode maps teveus's modes onto the CLI's own (default,
// accept-edits, auto, plan). Runs asynchronously so switching modes while a
// turn is active never blocks the UI thread.
func (c *Client) SetPermissionMode(mode string) error {
	c.mu.Lock()
	c.mode = mode
	c.mu.Unlock()
	if c.closed.Load() {
		return nil
	}
	go func() {
		if c.ready != nil {
			<-c.ready
		}
		if err := c.applyPermissionMode(mode); err != nil {
			c.emit(claude.ControlResult{Tag: "mode", Error: err.Error()})
		}
	}()
	return nil
}

func (c *Client) applyPermissionMode(mode string) error {
	c.mu.Lock()
	c.mode = mode
	sid := c.session
	c.mu.Unlock()
	if sid == "" || c.closed.Load() {
		return nil
	}
	_, err := c.request("session/set_mode", map[string]any{
		"sessionId": sid, "modeId": cliMode(mode),
	})
	return err
}

// cliMode is the CLI's name for one of teveus's permission modes.
func cliMode(mode string) string {
	switch mode {
	case "acceptEdits":
		return "accept-edits"
	case "auto", "bypassPermissions":
		return "auto"
	case "plan":
		return "plan"
	}
	return "default"
}

// SetModel switches the active model asynchronously so the UI stays responsive.
func (c *Client) SetModel(model string) error {
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()
	if c.closed.Load() {
		return nil
	}
	go func() {
		if c.ready != nil {
			<-c.ready
		}
		if err := c.applyModel(model); err != nil {
			c.emit(claude.ControlResult{Tag: "model", Error: err.Error()})
		}
	}()
	return nil
}

func (c *Client) applyModel(model string) error {
	c.mu.Lock()
	c.model = model
	sid := c.session
	useCfg := c.useConfigModel
	effort := c.effort
	c.mu.Unlock()
	if sid == "" || c.closed.Load() {
		return nil
	}
	if useCfg {
		res, err := c.request("session/set_config_option", map[string]any{
			"sessionId": sid, "configId": "model", "value": model,
		})
		if err != nil {
			// Fall back to session/set_model if the caller passed a full variant ID.
			_, err = c.request("session/set_model", map[string]any{"sessionId": sid, "modelId": model})
			return err
		}
		c.updateConfigOptions(res)
		if effort != "" {
			_ = c.applyEffort(effort)
		}
		return nil
	}
	_, err := c.request("session/set_model", map[string]any{"sessionId": sid, "modelId": model})
	return err
}

// SetEffort switches the live session's thinking level asynchronously via
// ACP's session/set_config_option without restarting the CLI or blocking UI.
func (c *Client) SetEffort(level string) error {
	c.mu.Lock()
	c.effort = level
	c.mu.Unlock()
	if c.closed.Load() {
		return nil
	}
	go func() {
		if c.ready != nil {
			<-c.ready
		}
		if err := c.applyEffort(level); err != nil {
			c.emit(claude.ControlResult{Tag: "effort", Error: err.Error()})
		}
	}()
	return nil
}

func (c *Client) applyEffort(level string) error {
	c.mu.Lock()
	c.effort = level
	sid := c.session
	levels := append([]string(nil), c.thoughtLevels...)
	c.mu.Unlock()
	if sid == "" || c.closed.Load() || level == "" || level == "auto" || len(levels) == 0 {
		return nil
	}
	clamped := clampThoughtLevel(level, levels)
	if clamped == "" {
		return nil
	}
	res, err := c.request("session/set_config_option", map[string]any{
		"sessionId": sid, "configId": "thought_level", "value": clamped,
	})
	if err == nil {
		c.updateConfigOptions(res)
	}
	return err
}

func (c *Client) updateConfigOptions(res json.RawMessage) {
	var out struct {
		ConfigOptions []configOption `json:"configOptions"`
	}
	if json.Unmarshal(res, &out) != nil || len(out.ConfigOptions) == 0 {
		return
	}
	for _, opt := range out.ConfigOptions {
		if opt.ID == "thought_level" {
			var levels []string
			for _, o := range opt.Options {
				levels = append(levels, o.Value)
			}
			c.mu.Lock()
			c.thoughtLevels = levels
			c.mu.Unlock()
			return
		}
	}
}

// Allow and Deny answer a permission request the UI asked the user about.
func (c *Client) Allow(req *claude.PermissionRequest, always bool) error {
	if always {
		c.mu.Lock()
		c.always[req.ToolName] = true
		c.mu.Unlock()
	}
	return c.answerPermission(req.RequestID, "allow")
}

func (c *Client) Deny(req *claude.PermissionRequest, reason string) error {
	return c.answerPermission(req.RequestID, "reject")
}

func (c *Client) Close() {
	if c.closed.Swap(true) {
		return
	}
	func() { _ = c.stdin.Close() }()
	done := make(chan struct{})
	go func() {
		c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutAfter(3 * time.Second):
		c.cmd.Process.Kill()
		<-done
	}
}

// answerPermission sends the user's choice. The CLI names its options; "allow"
// and "reject" are the ids it documents, and the first allow option is the one
// to select.
func (c *Client) answerPermission(requestID, choice string) error {
	c.mu.Lock()
	_, ok := c.perms[requestID]
	delete(c.perms, requestID)
	c.mu.Unlock()
	if !ok {
		return nil
	}
	option := "allow"
	if choice == "reject" {
		option = "reject"
	}
	// The CLI accepts either an option id it offered or the standard outcome.
	return c.respond(requestID, map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": option},
	})
}

var effortOrder = []string{"low", "medium", "high", "xhigh", "max"}

// parseSessionModels prefers ACP's clean configOptions[id="model"] list (one
// entry per base model, with per-model reasoning levels derived from
// availableModels) and falls back to availableModels when configOptions is absent.
func parseSessionModels(cfgOpts []configOption, avail []availableModel) (models []claude.ModelInfo, currentModel string, thoughtLevels []string, hasCfgModel bool) {
	var modelOpt *configOption
	for i := range cfgOpts {
		switch cfgOpts[i].ID {
		case "model":
			modelOpt = &cfgOpts[i]
			currentModel = cfgOpts[i].CurrentValue
			hasCfgModel = true
		case "thought_level":
			for _, o := range cfgOpts[i].Options {
				thoughtLevels = append(thoughtLevels, o.Value)
			}
		}
	}

	availSet := make(map[string]bool, len(avail))
	for _, m := range avail {
		availSet[m.ModelID] = true
	}

	if modelOpt != nil && len(modelOpt.Options) > 0 {
		for _, o := range modelOpt.Options {
			var reasoning []string
			for _, lvl := range effortOrder {
				if availSet[o.Value+"-"+lvl] {
					reasoning = append(reasoning, lvl)
				}
			}
			meta := ""
			if len(reasoning) > 1 {
				meta = reasoning[0] + "–" + reasoning[len(reasoning)-1]
			} else if len(reasoning) == 1 {
				meta = reasoning[0]
			}
			models = append(models, claude.ModelInfo{
				Value:       o.Value,
				DisplayName: o.Name,
				Group:       modelGroup(o.Value),
				Meta:        meta,
				Reasoning:   reasoning,
			})
		}
		return models, currentModel, thoughtLevels, true
	}

	for _, m := range avail {
		models = append(models, claude.ModelInfo{
			Value:       m.ModelID,
			DisplayName: m.Name,
			Group:       modelGroup(m.ModelID),
		})
	}
	return models, currentModel, thoughtLevels, false
}

func stopReasonSubtype(reason string) string {
	switch reason {
	case "", "end_turn":
		return "success"
	case "cancelled":
		return "error_during_execution"
	default:
		return reason
	}
}

func modelGroup(id string) string {
	switch {
	case strings.Contains(id, "orchestrator"):
		return "Antigravity · Orchestrator"
	case strings.HasPrefix(id, "gemini-4"):
		return "Antigravity · Gemini 4"
	case strings.HasPrefix(id, "gemini-"):
		return "Antigravity · Gemini"
	case strings.HasPrefix(id, "claude-"):
		return "Antigravity · Claude"
	case strings.HasPrefix(id, "gpt-"):
		return "Antigravity · OpenAI"
	default:
		return "Antigravity · Other"
	}
}

func clampThoughtLevel(want string, allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	have := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		have[a] = true
	}
	if have[want] {
		return want
	}
	rank := func(s string) int {
		for i, lvl := range effortOrder {
			if lvl == s {
				return i
			}
		}
		return -1
	}
	wantRank := rank(want)
	if wantRank < 0 {
		return allowed[len(allowed)-1]
	}
	best := ""
	for _, lvl := range effortOrder {
		if have[lvl] && rank(lvl) <= wantRank {
			best = lvl
		}
	}
	if best != "" {
		return best
	}
	return allowed[0]
}
