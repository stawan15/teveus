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
}

const maxSteps = 60

type permReply struct {
	allow, always bool
	reason        string
}

// Engine is a claude.Backend backed by direct provider API calls.
type Engine struct {
	opts Options
	box  *Toolbox

	evMu    sync.RWMutex
	events  chan claude.Event
	evClose bool

	queue chan string
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
		events:  make(chan claude.Event, 512),
		queue:   make(chan string, 32),
		ready:   make(chan struct{}),
		model:   opts.Model,
		mode:    opts.Mode,
		perms:   map[string]chan permReply{},
		always:  map[string]bool{},
		session: "api-" + hex.EncodeToString(id),
		creds:   map[string]Credential{},
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

func (e *Engine) init() {
	defer close(e.ready)
	e.loadProviders()
}

// Reload re-reads credentials and model lists (after /login) while keeping
// the conversation.
func (e *Engine) Reload() { go e.loadProviders() }

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
	for r := range results {
		for _, m := range r.models {
			models = append(models, modelInfo(r.p, m))
		}
	}
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

func (e *Engine) Send(text string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("engine closed")
	}
	select {
	case e.queue <- text:
		return nil
	default:
		return errors.New("too many queued messages")
	}
}

func (e *Engine) worker() {
	<-e.ready
	for text := range e.queue {
		e.turn(text)
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

func (e *Engine) turn(text string) {
	ctx, cancel := context.WithCancel(context.Background())
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
	e.history = append(e.history, Message{Role: "user", Text: text})
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.checkpoints = append(e.checkpoints, cp)
		e.cur = nil
		e.saveLocked()
		e.mu.Unlock()
	}()

	var total Usage
	steps := 0
	for steps < maxSteps {
		steps++
		e.mu.Lock()
		req := Request{Model: modelID, System: e.systemPrompt(), Messages: append([]Message(nil), e.history...), Tools: toolDefs()}
		e.mu.Unlock()

		streaming := false
		resp, err := client.Stream(ctx, req, func(c Chunk) {
			switch {
			case c.Text != "":
				if !streaming {
					streaming = true
					e.emit(claude.BlockStart{Kind: "text"})
				}
				e.emit(claude.TextDelta{Text: c.Text})
			case c.Thinking:
				e.emit(claude.BlockStart{Kind: "thinking"})
			case c.ToolStart != nil:
				e.emit(claude.BlockStart{Kind: "tool_use", ToolName: c.ToolStart.Name})
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				e.emit(claude.Result{Subtype: "error_during_execution", IsError: true})
			} else {
				e.emit(claude.Result{Subtype: "error", IsError: true, Text: err.Error()})
			}
			return
		}

		total.Input += resp.Usage.Input
		total.Output += resp.Usage.Output
		total.CacheRead += resp.Usage.CacheRead
		total.CacheWrite += resp.Usage.CacheWrite
		e.mu.Lock()
		e.cost += resp.Usage.Cost
		e.history = append(e.history, Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Reasoning: resp.Reasoning})
		e.mu.Unlock()

		var blocks []claude.ContentBlock
		if strings.TrimSpace(resp.Text) != "" {
			blocks = append(blocks, claude.ContentBlock{Type: "text", Text: resp.Text})
		}
		for _, tc := range resp.ToolCalls {
			blocks = append(blocks, claude.ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Input})
		}
		e.emit(claude.AssistantMessage{Blocks: blocks, Usage: &claude.Usage{
			InputTokens: resp.Usage.Input, OutputTokens: resp.Usage.Output,
			CacheReadInputTokens: resp.Usage.CacheRead, CacheCreationInputTokens: resp.Usage.CacheWrite,
		}})

		if len(resp.ToolCalls) == 0 {
			break
		}
		for _, tc := range resp.ToolCalls {
			out, isErr := e.runTool(ctx, tc)
			content, _ := json.Marshal(out)
			e.emit(claude.ToolResults{Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: tc.ID, Content: content, IsError: isErr}}})
			e.mu.Lock()
			e.history = append(e.history, Message{Role: "tool", ToolCallID: tc.ID, Result: out, IsError: isErr})
			e.mu.Unlock()
		}
		if ctx.Err() != nil {
			e.emit(claude.Result{Subtype: "error_during_execution", IsError: true})
			return
		}
		e.mu.Lock()
		stop := e.stopTurn
		e.mu.Unlock()
		if stop {
			break // the user denied a tool: hand control back to them
		}
	}

	e.mu.Lock()
	cost := e.cost
	e.mu.Unlock()
	e.emit(claude.Result{Subtype: "success", NumTurns: steps, CostUSD: cost,
		DurationMS: time.Since(start).Milliseconds(),
		Usage: claude.Usage{InputTokens: total.Input, OutputTokens: total.Output,
			CacheReadInputTokens: total.CacheRead, CacheCreationInputTokens: total.CacheWrite}})
}

// runTool applies the permission mode, asks the user when needed and runs
// the tool. Every call gets a result so the history stays valid.
func (e *Engine) runTool(ctx context.Context, tc ToolCall) (string, bool) {
	if ctx.Err() != nil {
		return "Interrupted by the user.", true
	}
	e.mu.Lock()
	skip := e.stopTurn
	e.mu.Unlock()
	if skip {
		return "Skipped: the user denied an earlier tool call.", true
	}
	t, ok := toolByName(tc.Name)
	if !ok {
		return fmt.Sprintf("Unknown tool %q.", tc.Name), true
	}

	e.mu.Lock()
	mode, always := e.mode, e.always[t.def.Name]
	e.mu.Unlock()
	if t.access != readOnly {
		switch {
		case mode == "plan":
			return "Plan mode is read-only. Describe the change in your plan instead of making it.", true
		case mode == "auto" || mode == "bypassPermissions" || always:
		case mode == "acceptEdits" && t.access == editsFiles:
		default:
			if r := e.ask(ctx, t, tc); !r.allow {
				e.mu.Lock()
				e.stopTurn = true
				e.mu.Unlock()
				return "The user denied this action. Stop and wait for their instructions.", true
			}
		}
	}

	in := parseInput(tc.Input)
	if t.access == editsFiles {
		e.mu.Lock()
		if e.cur != nil {
			e.cur.remember(e.box.abs(str(in, "file_path")))
		}
		e.mu.Unlock()
	}
	out, err := t.run(ctx, e.box, in)
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
	e.emit(&claude.PermissionRequest{
		RequestID: id, ToolName: t.def.Name, ToolUseID: tc.ID, Input: tc.Input,
		Description: desc, Suggestions: json.RawMessage(`["session"]`),
	})
	select {
	case r := <-ch:
		if r.always {
			e.mu.Lock()
			e.always[t.def.Name] = true
			e.mu.Unlock()
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
	hist := append([]Message(nil), e.history...)
	e.mu.Unlock()
	if len(hist) == 0 {
		e.emit(claude.AssistantMessage{Blocks: []claude.ContentBlock{{Type: "text", Text: "Nothing to compact yet."}}})
		e.emit(claude.Result{Subtype: "success"})
		return
	}
	hist = append(hist, Message{Role: "user", Text: "Summarise this conversation so work can continue from the summary alone: the goal, key decisions, files changed or inspected, current state, and next steps. Be concise and specific."})
	resp, err := client.Stream(ctx, Request{Model: modelID, System: "You write precise handover summaries of coding sessions.", Messages: hist}, func(Chunk) {})
	if err != nil {
		e.emit(claude.Result{Subtype: "error", IsError: true, Text: "compact failed: " + err.Error()})
		return
	}
	e.mu.Lock()
	e.history = []Message{{Role: "user", Text: "Summary of our conversation so far:\n\n" + resp.Text},
		{Role: "assistant", Text: "Understood. I'll continue from this summary."}}
	e.cost += resp.Usage.Cost
	e.mu.Unlock()
	e.emit(claude.AssistantMessage{Blocks: []claude.ContentBlock{{Type: "text", Text: "Conversation compacted.\n\n" + resp.Text}}})
	e.emit(claude.Result{Subtype: "success", DurationMS: time.Since(start).Milliseconds()})
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
