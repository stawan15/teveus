// Package antigravity drives Google's Antigravity CLI (`agy`) over its Agent
// Client Protocol (ACP) stdio server.
//
// The CLI is started as `agy acp`, which speaks JSON-RPC 2.0 on stdin/stdout.
// thereof.acp:
//
//	initialize                        -> protocol version, the models it offers
//	session/new                       -> a session id
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
	"sync"
	"sync/atomic"
	"time"

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
}

// Client is one `agy acp` process.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan claude.Event
	stderr *tailBuffer

	writeMu sync.Mutex
	nextID  atomic.Int64
	closed  atomic.Bool

	mu      sync.Mutex
	session string
	models  []claude.ModelInfo
	mode    string // permission mode, as the UI names it
	waiting map[string]chan *rpcMessage

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
		perms: map[string]*claude.PermissionRequest{}, always: map[string]bool{},
		mode: opts.PermissionMode, waiting: map[string]chan *rpcMessage{},
	}
	go c.read(stdout)
	if err := c.handshake(opts); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Events() <-chan claude.Event { return c.events }

// handshake initializes the protocol and opens a session.
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
	params := map[string]any{"cwd": opts.Cwd, "mcpServers": []any{}}
	if opts.Resume != "" {
		params["resumeSessionId"] = opts.Resume
	}
	res, err := c.request("session/new", params)
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}
	var out struct {
		SessionID string `json:"sessionId"`
		Models    *struct {
			AvailableModels []struct {
				ModelID string `json:"modelId"`
				Name    string `json:"name"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return err
	}
	if out.SessionID == "" {
		return errors.New("the CLI opened no session")
	}
	c.mu.Lock()
	c.session = out.SessionID
	if out.Models != nil {
		for _, m := range out.Models.AvailableModels {
			c.models = append(c.models, claude.ModelInfo{Value: m.ModelID, DisplayName: m.Name})
		}
	}
	models := append([]claude.ModelInfo(nil), c.models...)
	c.mu.Unlock()

	c.emit(claude.Ready{Models: models, PermissionMode: permissionModeLabel(opts.PermissionMode)})
	c.emit(claude.Init{SessionID: out.SessionID, Model: opts.Model, Cwd: opts.Cwd,
		PermissionMode: permissionModeLabel(opts.PermissionMode)})

	// The session starts in the CLI's own default; apply the user's choice,
	// and the model they picked in teveus rather than the CLI's last one.
	if opts.PermissionMode != "" {
		c.SetPermissionMode(opts.PermissionMode)
	}
	if opts.Model != "" {
		c.setModel(opts.Model)
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
	sid := c.session
	c.mu.Unlock()

	go func() {
		res, err := c.request("session/prompt", map[string]any{"sessionId": sid, "prompt": prompt})
		if err != nil {
			c.emit(claude.Result{Subtype: "error", IsError: true, Text: err.Error()})
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
			Subtype: out.StopReason,
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

// Interrupt cancels the turn that is running.
func (c *Client) Interrupt() error {
	c.mu.Lock()
	sid := c.session
	c.mu.Unlock()
	_, err := c.request("session/cancel", map[string]any{"sessionId": sid})
	return err
}

// SetPermissionMode maps teveus's modes onto the CLI's own (default,
// accept-edits, auto, plan). In "default" the CLI asks before each tool and the
// UI shows its usual dialog; the others answer for themselves.
func (c *Client) SetPermissionMode(mode string) error {
	c.mu.Lock()
	c.mode = mode
	sid := c.session
	c.mu.Unlock()
	if sid == "" {
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

func (c *Client) SetModel(model string) error { return c.setModel(model) }

func (c *Client) setModel(model string) error {
	c.mu.Lock()
	sid, ok := c.session, c.session != ""
	c.mu.Unlock()
	if !ok {
		return nil
	}
	_, err := c.request("session/set_model", map[string]any{"sessionId": sid, "modelId": model})
	return err
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
