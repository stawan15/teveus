package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stawan15/teveus/internal/claude"
)

type Options struct {
	Cwd   string
	Model string // "provider/model-id"
	Mode  string // permission mode
	Store *Store
	Style string // answer-style instructions (the concise saver)

	SessionDir string // where conversations are saved; empty disables
	Resume     string // session ID to continue
	ConfigDir  string // teveus's settings: MCP servers, permission rules
	Effort     string // reasoning effort ("" = the model's default)

	// Attribution lets the model credit itself in commits and pull requests
	// (Co-Authored-By, "Generated with…"). Off by default: commits are the user's.
	Attribution bool
}

const (
	maxSteps    = 60
	maxSubSteps = 30 // for a Task subagent
)

type permReply struct {
	allow, always bool
	reason        string
}

// Engine is a claude.Backend backed by direct provider API calls.
type Engine struct {
	opts Options
	box  *Toolbox
	mcp  *mcpManager
	perm *permissions

	evMu    sync.RWMutex
	events  chan claude.Event
	evClose bool

	queue chan userTurn
	ready chan struct{}

	mu       sync.Mutex
	model    string
	mode     string
	history  []Message
	cancel   context.CancelFunc
	perms    map[string]chan permReply
	always   map[string]bool
	cost     float64
	nextID   int
	closed   bool
	session  string
	creds    map[string]Credential
	ctxLen   map[string]int // context window by "provider/model", when known
	stopTurn bool

	cur         *checkpoint   // edits of the running turn
	checkpoints []*checkpoint // one per finished turn, for /undo
}

var _ claude.Backend = (*Engine)(nil)

func Start(opts Options) (*Engine, error) {
	id := make([]byte, 6)
	rand.Read(id)
	e := &Engine{
		opts:    opts,
		box:     NewToolbox(opts.Cwd),
		mcp:     newMCPManager(opts.Cwd, opts.ConfigDir),
		perm:    newPermissions(opts.ConfigDir, opts.Cwd),
		events:  make(chan claude.Event, 512),
		queue:   make(chan userTurn, 32),
		ready:   make(chan struct{}),
		model:   opts.Model,
		mode:    opts.Mode,
		perms:   map[string]chan permReply{},
		always:  map[string]bool{},
		session: "api-" + hex.EncodeToString(id),
		creds:   map[string]Credential{},
		ctxLen:  map[string]int{},
	}
	if opts.Resume != "" {
		s, err := LoadSession(opts.SessionDir, opts.Resume)
		if err != nil {
			return nil, fmt.Errorf("resume: %w", err)
		}
		e.session, e.history = s.ID, s.History
		if e.model == "" {
			e.model = s.Model
		}
	}
	go e.init()
	go e.worker()
	return e, nil
}

// Undo reverts the last turn: files it wrote or edited are restored and the
// exchange is removed from the conversation. Shell commands aren't undone.
func (e *Engine) Undo() ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		return nil, errors.New("wait for the current turn to finish")
	}
	if len(e.checkpoints) == 0 {
		return nil, errors.New("nothing to undo")
	}
	cp := e.checkpoints[len(e.checkpoints)-1]
	e.checkpoints = e.checkpoints[:len(e.checkpoints)-1]
	restored, err := cp.restore()
	if cp.historyLen <= len(e.history) {
		e.history = e.history[:cp.historyLen]
	}
	e.saveLocked()
	return restored, err
}

// AddContext puts text into the conversation without starting a turn
// (used for the output of "!command").
func (e *Engine) AddContext(text string) {
	e.mu.Lock()
	e.history = append(e.history, Message{Role: "user", Text: text})
	e.saveLocked()
	e.mu.Unlock()
}

// saveLocked persists the conversation; call with e.mu held.
func (e *Engine) saveLocked() {
	SaveSession(e.opts.SessionDir, Session{ID: e.session, Cwd: e.opts.Cwd, Model: e.model,
		Title: sessionTitle(e.history), Updated: time.Now(), History: e.history})
}

func (e *Engine) Events() <-chan claude.Event { return e.events }

func (e *Engine) emit(ev claude.Event) {
	e.evMu.RLock()
	defer e.evMu.RUnlock()
	if !e.evClose {
		e.events <- ev
	}
}

// init loads models and starts MCP servers; turns wait for both, so the
// first one already has every tool.
func (e *Engine) init() {
	defer close(e.ready)
	done := make(chan struct{})
	go func() {
		e.mcp.start(true)
		close(done)
	}()
	e.loadProviders()
	<-done
}

// Reload re-reads credentials and model lists (after /login) while keeping
// the conversation.
func (e *Engine) Reload() { go e.loadProviders() }

// SetAttribution changes whether commits may credit the AI, from the next request on.
func (e *Engine) SetAttribution(on bool) {
	e.mu.Lock()
	e.opts.Attribution = on
	e.mu.Unlock()
}

// SetEffort changes the reasoning effort from the next request on.
func (e *Engine) SetEffort(effort string) {
	e.mu.Lock()
	e.opts.Effort = effort
	e.mu.Unlock()
}

// SetStyle changes the answer-style instructions from the next request on.
func (e *Engine) SetStyle(style string) {
	e.mu.Lock()
	e.opts.Style = style
	e.mu.Unlock()
}

// loadProviders loads credentials and every connected provider's model list.
func (e *Engine) loadProviders() {
	type result struct {
		p      Provider
		models []Model
	}
	var wg sync.WaitGroup
	results := make(chan result, len(Providers))
	for _, p := range Providers {
		cred, src, _ := e.opts.Store.Lookup(p)
		if src == FromNone || (p.Custom && cred.BaseURL == "") {
			e.mu.Lock()
			delete(e.creds, p.ID)
			e.mu.Unlock()
			continue
		}
		e.mu.Lock()
		e.creds[p.ID] = cred
		e.mu.Unlock()
		wg.Add(1)
		go func(p Provider, cred Credential) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			ms, err := NewClient(p, cred).Models(ctx)
			if err == nil {
				results <- result{p, ms}
			}
		}(p, cred)
	}
	wg.Wait()
	close(results)

	var models []claude.ModelInfo
	ctxLen := map[string]int{}
	for r := range results {
		for _, m := range r.models {
			models = append(models, modelInfo(r.p, m))
			if m.Context > 0 {
				ctxLen[r.p.ID+"/"+m.ID] = m.Context
			}
		}
	}
	e.mu.Lock()
	e.ctxLen = ctxLen
	e.mu.Unlock()
	// Groups: well-known makers first, then the rest alphabetically; within
	// a group the provider's order (newest first) is kept.
	sort.SliceStable(models, func(i, j int) bool {
		gi, gj := models[i].Group, models[j].Group
		if gi == gj {
			return false
		}
		ri, rj := groupRank(gi), groupRank(gj)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(gi) < strings.ToLower(gj)
	})

	e.mu.Lock()
	model, mode := e.model, e.mode
	e.mu.Unlock()
	e.emit(claude.Ready{
		PermissionMode: mode,
		Models:         models,
		Commands:       []claude.Command{{Name: "compact", Description: "Summarise the conversation to free context"}},
	})
	e.emit(claude.Init{SessionID: e.session, Model: model, Cwd: e.opts.Cwd, PermissionMode: mode})
}

// userTurn is a queued user message.
type userTurn struct {
	text   string
	images []claude.Image
}

func (e *Engine) Send(text string) error { return e.SendImages(text, nil) }

func (e *Engine) SendImages(text string, images []claude.Image) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("engine closed")
	}
	select {
	case e.queue <- userTurn{text, images}:
		return nil
	default:
		return errors.New("too many queued messages")
	}
}

func (e *Engine) worker() {
	<-e.ready
	for t := range e.queue {
		e.turn(t.text, t.images)
	}
}

func (e *Engine) Interrupt() error {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	return nil
}

func (e *Engine) SetPermissionMode(mode string) error {
	e.mu.Lock()
	e.mode = mode
	e.mu.Unlock()
	go e.emit(claude.Status{PermissionMode: mode})
	return nil
}

func (e *Engine) SetModel(model string) error {
	e.mu.Lock()
	e.model = model
	e.mu.Unlock()
	return nil
}

func (e *Engine) Allow(req *claude.PermissionRequest, always bool) error {
	return e.reply(req.RequestID, permReply{allow: true, always: always})
}

func (e *Engine) Deny(req *claude.PermissionRequest, reason string) error {
	return e.reply(req.RequestID, permReply{reason: reason})
}

func (e *Engine) reply(id string, r permReply) error {
	e.mu.Lock()
	ch := e.perms[id]
	delete(e.perms, id)
	e.mu.Unlock()
	if ch != nil {
		ch <- r
	}
	return nil
}

func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	close(e.queue)
	go e.mcp.close()
	go func() {
		e.emit(claude.Exited{Requested: true})
		e.evMu.Lock()
		e.evClose = true
		close(e.events)
		e.evMu.Unlock()
	}()
}

// client resolves "provider/model" to an API client and the model ID.
func (e *Engine) client() (Client, string, error) {
	e.mu.Lock()
	model := e.model
	e.mu.Unlock()
	if model == "" {
		return nil, "", errors.New("no model selected: pick one with /model (connect a provider with /login)")
	}
	pid, mid, ok := strings.Cut(model, "/")
	p, known := ProviderByID(pid)
	if !ok || !known {
		return nil, "", fmt.Errorf("unknown model %q: use provider/model, e.g. openai/gpt-5.4-mini", model)
	}
	e.mu.Lock()
	cred, has := e.creds[pid]
	e.mu.Unlock()
	if !has {
		c, src, _ := e.opts.Store.Lookup(p)
		if src == FromNone {
			return nil, "", fmt.Errorf("%s is not connected: use /login", p.Name)
		}
		cred = c
	}
	return NewClient(p, cred), mid, nil
}

func (e *Engine) turn(text string, images []claude.Image) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = withRetryNotice(ctx, func(wait time.Duration, why string) {
		e.emit(claude.Status{Status: fmt.Sprintf("retrying in %ds (%s)", int(wait.Round(time.Second).Seconds()), why)})
	})
	e.mu.Lock()
	e.cancel, e.stopTurn = cancel, false
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		e.cancel = nil
		e.mu.Unlock()
	}()
	start := time.Now()

	client, modelID, err := e.client()
	if err != nil {
		e.emit(claude.Result{Subtype: "error", IsError: true, Text: err.Error()})
		return
	}
	if strings.TrimSpace(text) == "/compact" {
		e.compact(ctx, client, modelID, start)
		return
	}

	e.mu.Lock()
	cp := &checkpoint{historyLen: len(e.history), files: map[string][]byte{}}
	e.cur = cp
	e.history = append(e.history, Message{Role: "user", Text: text, Images: images})
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.checkpoints = append(e.checkpoints, cp)
		e.cur = nil
		e.saveLocked()
		e.mu.Unlock()
	}()

	steps, total, err := e.loop(ctx, client, modelID, mainConv{e}, e.tools(true), "")
	e.mu.Lock()
	cost := e.cost
	e.mu.Unlock()
	// Errors report what the turn used too: steps before the failure were paid for.
	res := claude.Result{Subtype: "success", NumTurns: steps, CostUSD: cost,
		DurationMS: time.Since(start).Milliseconds(),
		Usage: claude.Usage{InputTokens: total.Input, OutputTokens: total.Output,
			CacheReadInputTokens: total.CacheRead, CacheCreationInputTokens: total.CacheWrite}}
	switch {
	case err != nil && ctx.Err() != nil:
		res.Subtype, res.IsError = "error_during_execution", true
	case err != nil:
		res.Subtype, res.IsError, res.Text = "error", true, err.Error()
	}
	e.emit(res)
}

// conversation is the history a loop reads and extends: the main one, or a
// subagent's own.
type conversation interface {
	system() string
	messages() []Message
	add(Message)
}

type mainConv struct{ e *Engine }

func (c mainConv) system() string {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	return c.e.systemPrompt()
}

func (c mainConv) messages() []Message {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	return append([]Message(nil), c.e.history...)
}

func (c mainConv) add(m Message) {
	c.e.mu.Lock()
	c.e.history = append(c.e.history, m)
	c.e.mu.Unlock()
}

// loop runs model steps and their tool calls until the model stops calling
// tools. parent is the Task call a subagent runs under ("" for the main
// conversation); its events carry it so the UI nests them.
func (e *Engine) loop(ctx context.Context, client Client, modelID string, conv conversation, toolset []tool, parent string) (int, Usage, error) {
	var total Usage
	steps := 0
	limit := maxSteps
	if parent != "" {
		limit = maxSubSteps
	}
	lastContext := 0
	compacted := false
	for steps < limit {
		steps++
		// The main conversation compacts itself before it outgrows the
		// model's context, and once more if the API says it already has.
		if window := e.contextLimit(); parent == "" && window > 0 && float64(lastContext) > autoCompactAt*float64(window) && !compacted {
			if err := e.autoCompact(ctx, client, modelID, steps > 1, lastContext); err != nil {
				return steps, total, err
			}
			compacted = true
		}
		e.mu.Lock()
		effort := e.opts.Effort
		e.mu.Unlock()
		if parent != "" && effort != "" {
			effort = "low" // research subagents do simple, many-step work
		}
		req := Request{Model: modelID, System: conv.system(), Messages: conv.messages(), Tools: defsOf(toolset), Effort: effort}

		streaming := false
		stream := func(c Chunk) {
			switch {
			case c.Text != "" && parent == "":
				if !streaming {
					streaming = true
					e.emit(claude.BlockStart{Kind: "text"})
				}
				e.emit(claude.TextDelta{Text: c.Text})
			case c.Thinking && parent == "":
				e.emit(claude.BlockStart{Kind: "thinking"})
			case c.ToolStart != nil:
				e.emit(claude.BlockStart{Kind: "tool_use", ToolName: c.ToolStart.Name})
			}
		}
		resp, err := client.Stream(ctx, req, stream)
		if err != nil && parent == "" && !compacted && ctx.Err() == nil && overflowError(err) {
			if err := e.autoCompact(ctx, client, modelID, steps > 1, 0); err != nil {
				return steps, total, err
			}
			compacted = true
			req.Messages = conv.messages()
			resp, err = client.Stream(ctx, req, stream)
		}
		if err != nil {
			return steps, total, err
		}
		lastContext = resp.Usage.Input + resp.Usage.CacheRead + resp.Usage.CacheWrite

		total.Input += resp.Usage.Input
		total.Output += resp.Usage.Output
		total.CacheRead += resp.Usage.CacheRead
		total.CacheWrite += resp.Usage.CacheWrite
		e.mu.Lock()
		e.cost += resp.Usage.Cost
		e.mu.Unlock()
		conv.add(Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Reasoning: resp.Reasoning})

		var blocks []claude.ContentBlock
		if strings.TrimSpace(resp.Text) != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "text", Text: resp.Text})
		}
		for _, tc := range resp.ToolCalls {
			blocks = append(blocks, claude.ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Input})
		}
		e.emit(claude.AssistantMessage{Parent: parent, Blocks: blocks, Usage: &claude.Usage{
			InputTokens: resp.Usage.Input, OutputTokens: resp.Usage.Output,
			CacheReadInputTokens: resp.Usage.CacheRead, CacheCreationInputTokens: resp.Usage.CacheWrite,
		}})

		if len(resp.ToolCalls) == 0 {
			break
		}
		var results []toolResult
		if resp.Stop == "max_tokens" || resp.Stop == "length" {
			// The reply hit the output limit, so the last call's input may be
			// cut short: run nothing and ask for it again, smaller.
			for range resp.ToolCalls {
				results = append(results, toolResult{"Not run: your reply reached the output limit, so this call may be incomplete. Send it again, splitting large content (like a big file) into several smaller Write/Edit calls.", true})
			}
		} else {
			results = e.runTools(ctx, resp.ToolCalls, toolset)
		}
		for i, tc := range resp.ToolCalls {
			r := results[i]
			content, _ := json.Marshal(r.out)
			e.emit(claude.ToolResults{Parent: parent, Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: tc.ID, Content: content, IsError: r.isErr}}})
			conv.add(Message{Role: "tool", ToolCallID: tc.ID, Result: r.out, IsError: r.isErr})
		}
		if ctx.Err() != nil {
			return steps, total, ctx.Err()
		}
		e.mu.Lock()
		stop := e.stopTurn
		e.mu.Unlock()
		if stop {
			break // the user denied a tool: hand control back to them
		}
	}
	return steps, total, nil
}

type toolResult struct {
	out   string
	isErr bool
}

// runTools runs one step's tool calls. Read-only calls with no prompt to
// show (reads, searches, subagents) run in parallel; anything else runs in
// order, so permission prompts come one at a time.
func (e *Engine) runTools(ctx context.Context, calls []ToolCall, toolset []tool) []toolResult {
	results := make([]toolResult, len(calls))
	parallel := len(calls) > 1
	for _, tc := range calls {
		if t, ok := findTool(toolset, tc.Name); !ok || t.access != readOnly {
			parallel = false
		}
	}
	if !parallel {
		for i, tc := range calls {
			out, isErr := e.runTool(ctx, tc, toolset)
			results[i] = toolResult{out, isErr}
		}
		return results
	}
	var wg sync.WaitGroup
	for i, tc := range calls {
		wg.Add(1)
		go func(i int, tc ToolCall) {
			defer wg.Done()
			out, isErr := e.runTool(ctx, tc, toolset)
			results[i] = toolResult{out, isErr}
		}(i, tc)
	}
	wg.Wait()
	return results
}

// runTool applies the permission mode, asks the user when needed and runs
// the tool. Every call gets a result so the history stays valid.
func (e *Engine) runTool(ctx context.Context, tc ToolCall, toolset []tool) (string, bool) {
	if ctx.Err() != nil {
		return "Interrupted by the user.", true
	}
	e.mu.Lock()
	skip := e.stopTurn
	e.mu.Unlock()
	if skip {
		return "Skipped: the user denied an earlier tool call.", true
	}
	t, ok := findTool(toolset, tc.Name)
	if !ok {
		return fmt.Sprintf("Unknown tool %q.", tc.Name), true
	}

	in := parseInput(tc.Input)
	rule := e.perm.check(t.def.Name, in)
	if rule == "deny" {
		return "Blocked by the user's permission rules. Don't retry it or work around it; tell the user what you needed.", true
	}
	outside := false
	for _, p := range e.toolPaths(t.def.Name, in) {
		if e.sensitive(p) {
			return p + " holds credentials, so teveus's file tools can't touch it in any mode. Don't try another way; tell the user what you needed.", true
		}
		outside = outside || !e.inProject(p)
	}
	e.mu.Lock()
	mode, always := e.mode, e.always[t.def.Name]
	e.mu.Unlock()
	// Reads inside the project run freely; everything else is checked.
	if t.access != readOnly || outside {
		switch {
		case mode == "plan" && t.access != readOnly && t.access != network:
			return "Plan mode is read-only. Describe the change in your plan instead of making it.", true
		case mode == "auto" || mode == "bypassPermissions" || rule == "allow":
		case always && !outside:
		case mode == "acceptEdits" && t.access == editsFiles && !outside:
		default:
			if r := e.ask(ctx, t, tc); !r.allow {
				e.mu.Lock()
				e.stopTurn = true
				e.mu.Unlock()
				return "The user denied this action. Stop and wait for their instructions.", true
			}
		}
	}

	if t.access == editsFiles {
		e.mu.Lock()
		if e.cur != nil {
			e.cur.remember(e.box.abs(str(in, "file_path")))
		}
		e.mu.Unlock()
	}
	var out string
	var err error
	if t.run == nil && t.def.Name == "Task" {
		out, err = e.runTask(ctx, tc.ID, in)
	} else {
		out, err = t.run(ctx, e.box, in)
	}
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

func (e *Engine) ask(ctx context.Context, t tool, tc ToolCall) permReply {
	e.mu.Lock()
	e.nextID++
	id := fmt.Sprintf("perm-%d", e.nextID)
	ch := make(chan permReply, 1)
	e.perms[id] = ch
	e.mu.Unlock()

	in := parseInput(tc.Input)
	desc := str(in, "description")
	if p := str(in, "file_path"); p != "" {
		desc = p
	}
	// "Always" means: every edit for the rest of the session, or a saved
	// rule for this kind of call (see suggestRule).
	rule, label := suggestRule(t.def.Name, in)
	var suggestions json.RawMessage
	switch {
	case t.access == editsFiles:
		suggestions, label = json.RawMessage(`["session"]`), "allow all edits this session"
	case rule != "":
		suggestions = json.RawMessage(`["rule"]`)
	}
	e.emit(&claude.PermissionRequest{
		RequestID: id, ToolName: t.def.Name, ToolUseID: tc.ID, Input: tc.Input,
		Description: desc, Suggestions: suggestions, AlwaysLabel: label,
	})
	select {
	case r := <-ch:
		if r.always {
			if t.access == editsFiles {
				e.mu.Lock()
				for _, name := range []string{"Edit", "Write"} {
					e.always[name] = true
				}
				e.mu.Unlock()
			} else if rule != "" {
				// If saving fails the only cost is being asked again.
				e.perm.AddProjectRule(rule, false)
			}
		}
		return r
	case <-ctx.Done():
		e.mu.Lock()
		delete(e.perms, id)
		e.mu.Unlock()
		return permReply{}
	}
}

// compact replaces the history with a model-written summary, which keeps
// long sessions cheap: every request resends the whole history.
func (e *Engine) compact(ctx context.Context, client Client, modelID string, start time.Time) {
	e.mu.Lock()
	empty := len(e.history) == 0
	e.mu.Unlock()
	if empty {
		e.emit(claude.AssistantMessage{Blocks: []claude.ContentBlock{{Type: "text", Text: "Nothing to compact yet."}}})
		e.emit(claude.Result{Subtype: "success"})
		return
	}
	summary, err := e.compactHistory(ctx, client, modelID, false, false)
	if err != nil {
		e.emit(claude.Result{Subtype: "error", IsError: true, Text: "compact failed: " + err.Error()})
		return
	}
	e.emit(claude.AssistantMessage{Blocks: []claude.ContentBlock{{Type: "text", Text: "Conversation compacted.\n\n" + summary}}})
	e.emit(claude.Result{Subtype: "success", DurationMS: time.Since(start).Milliseconds()})
}

// compactHistory summarises the conversation and makes the summary the new
// history. keepLast keeps the newest user message out of the summary (a turn
// that hasn't started yet); midTurn adds a nudge to carry on with the task.
func (e *Engine) compactHistory(ctx context.Context, client Client, modelID string, keepLast, midTurn bool) (string, error) {
	e.mu.Lock()
	hist := append([]Message(nil), e.history...)
	e.mu.Unlock()
	var kept []Message
	if n := len(hist); keepLast && n > 0 && hist[n-1].Role == "user" {
		hist, kept = hist[:n-1], append([]Message(nil), hist[n-1])
	}
	hist = append(hist, Message{Role: "user", Text: "Summarise this conversation so work can continue from the summary alone: the goal, key decisions, files changed or inspected, current state, and next steps. Be concise and specific."})
	resp, err := client.Stream(ctx, Request{Model: modelID, System: "You write precise handover summaries of coding sessions.", Messages: hist}, func(Chunk) {})
	if err != nil {
		return "", err
	}
	next := []Message{{Role: "user", Text: "Summary of our conversation so far:\n\n" + resp.Text},
		{Role: "assistant", Text: "Understood. I'll continue from this summary."}}
	if midTurn {
		next = append(next, Message{Role: "user", Text: "Continue the task from where you left off."})
	}
	next = append(next, kept...)
	e.mu.Lock()
	e.history = next
	e.cost += resp.Usage.Cost
	// Undo can't bring back what was summarised: keep file restores only.
	e.checkpoints = nil
	if e.cur != nil {
		e.cur.historyLen = len(next) - len(kept)
	}
	e.saveLocked()
	e.mu.Unlock()
	return resp.Text, nil
}

// autoCompact compacts the main conversation mid-task and tells the UI.
func (e *Engine) autoCompact(ctx context.Context, client Client, modelID string, midTurn bool, pre int) error {
	e.emit(claude.Status{Status: "compacting"})
	if _, err := e.compactHistory(ctx, client, modelID, !midTurn, midTurn); err != nil {
		return fmt.Errorf("the conversation is too long and compacting it failed: %w", err)
	}
	e.emit(claude.Status{})
	e.emit(claude.Compacted{Auto: true, PreTokens: pre})
	return nil
}

// contextLimit is the model's context window, when its provider said.
func (e *Engine) contextLimit() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ctxLen[e.model]
}

// autoCompactAt is the share of the context window that triggers compaction.
const autoCompactAt = 0.85

// overflowError reports whether an API error says the prompt was too long.
func overflowError(err error) bool {
	s := strings.ToLower(err.Error())
	for _, k := range []string{"context_length_exceeded", "prompt is too long", "maximum context length", "context window", "too many tokens", "input is too long", "reduce the length"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// modelInfo shapes a model for the picker: a short name, the full name, a
// group (provider, and maker for routers) and context/price metadata.
func modelInfo(p Provider, m Model) claude.ModelInfo {
	info := claude.ModelInfo{Value: p.ID + "/" + m.ID, DisplayName: m.ID, Group: p.Name}
	name := m.Name
	if vendor, short, ok := strings.Cut(m.ID, "/"); ok {
		// Routers name models "vendor/model": group by vendor.
		info.DisplayName = short
		info.Group = p.Name + " · " + vendorName(vendor)
		if _, after, ok := strings.Cut(name, ": "); ok {
			name = after
		}
	}
	if name != m.ID && name != info.DisplayName {
		info.Description = name
	}
	var meta []string
	if m.Context > 0 {
		meta = append(meta, fmtContext(m.Context))
	}
	if m.Priced {
		if m.In == 0 && m.Out == 0 {
			info.Free = true
		} else {
			meta = append(meta, fmtPrice(m.In)+"/"+fmtPrice(m.Out))
		}
	}
	info.Meta = strings.Join(meta, " · ")
	return info
}

// majorVendors are listed first in the picker, in this order.
var majorVendors = []string{"Anthropic", "OpenAI", "Google", "xAI", "DeepSeek", "Qwen", "Moonshot", "Z.ai", "MiniMax", "Mistral", "Meta"}

func groupRank(group string) int {
	_, vendor, found := strings.Cut(group, " · ")
	if !found {
		vendor = group
	}
	for i, v := range majorVendors {
		if v == vendor || strings.HasPrefix(group, v) {
			return i
		}
	}
	return len(majorVendors)
}

var vendorNames = map[string]string{
	"openai": "OpenAI", "anthropic": "Anthropic", "google": "Google", "meta-llama": "Meta",
	"mistralai": "Mistral", "x-ai": "xAI", "deepseek": "DeepSeek", "qwen": "Qwen",
	"z-ai": "Z.ai", "moonshotai": "Moonshot", "nvidia": "NVIDIA", "cohere": "Cohere",
	"minimax": "MiniMax", "microsoft": "Microsoft", "amazon": "Amazon", "perplexity": "Perplexity",
}

func vendorName(v string) string {
	v = strings.TrimLeft(v, "~")
	if n, ok := vendorNames[v]; ok {
		return n
	}
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}

func fmtContext(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M ctx"
	case n >= 1000:
		return fmt.Sprintf("%dk ctx", n/1000)
	}
	return fmt.Sprintf("%d ctx", n)
}

// fmtPrice formats USD per million tokens.
func fmtPrice(x float64) string {
	switch {
	case x >= 10:
		return fmt.Sprintf("$%.0f", x)
	case x >= 1:
		return strings.TrimSuffix(fmt.Sprintf("$%.1f", x), ".0")
	}
	return fmt.Sprintf("$%.2f", x)
}
