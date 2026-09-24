// Package claude drives a `claude` CLI process over its stream-json protocol.
//
// The CLI is started in print mode with stream-json on both stdin and stdout.
// User turns and control requests (interrupt, mode/model changes) are written
// as JSON lines; everything the CLI emits is decoded into the Event types in
// events.go and delivered on Events().
package claude

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

type Options struct {
	Binary         string // defaults to "claude"
	Cwd            string
	Model          string
	PermissionMode string
	Resume         string   // session ID to resume
	Continue       bool     // continue the most recent session in Cwd
	AppendPrompt   string   // appended to Claude Code's system prompt
	DisallowTools  []string // built-in tools to leave out of the context
	Effort         string   // reasoning effort: low, medium, high, xhigh, max ("" = the CLI's own setting)
	NoAttribution  bool     // ask the CLI not to add Co-Authored-By / "Generated with" lines to commits and PRs
	ExtraArgs      []string
}

type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event
	stderr *tailBuffer

	writeMu sync.Mutex
	nextID  atomic.Int64
	closed  atomic.Bool
}

func Start(opts Options) (*Client, error) {
	bin := opts.Binary
	if bin == "" {
		bin = "claude"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		// Route permission prompts back to us as can_use_tool control requests.
		"--permission-prompt-tool", "stdio",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	} else if opts.Continue {
		args = append(args, "--continue")
	}
	if opts.AppendPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendPrompt)
	}
	if len(opts.DisallowTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(opts.DisallowTools, ","))
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.NoAttribution {
		// The CLI's documented "attribution" setting: empty text hides it.
		args = append(args, "--settings", `{"attribution":{"commit":"","pr":""}}`)
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.Command(bin, args...)
	cmd.Dir = opts.Cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan Event, 256),
		stderr: &tailBuffer{max: 4096},
	}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	go c.readLoop(stdout)

	if err := c.control("init", map[string]any{"subtype": "initialize"}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Events delivers decoded CLI output. It is closed after an Exited event.
func (c *Client) Events() <-chan Event { return c.events }

// Send submits a user turn. The CLI queues it if a turn is already running.
func (c *Client) Send(text string) error {
	return c.write(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
}

// SendImages sends a user turn with pictures before the text, the order the
// API recommends.
func (c *Client) SendImages(text string, images []Image) error {
	var content []map[string]any
	for _, img := range images {
		content = append(content, map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": img.MediaType, "data": base64.StdEncoding.EncodeToString(img.Data)}})
	}
	content = append(content, map[string]any{"type": "text", "text": text})
	return c.write(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": content},
	})
}

func (c *Client) Interrupt() error {
	return c.control("", map[string]any{"subtype": "interrupt"})
}

func (c *Client) SetPermissionMode(mode string) error {
	return c.control("mode", map[string]any{"subtype": "set_permission_mode", "mode": mode})
}

func (c *Client) SetModel(model string) error {
	return c.control("model", map[string]any{"subtype": "set_model", "model": model})
}

// Allow approves a can_use_tool request. When always is true the CLI's own
// permission suggestions (e.g. "allow this command for the session") are applied.
func (c *Client) Allow(req *PermissionRequest, always bool) error {
	resp := map[string]any{"behavior": "allow", "updatedInput": req.Input}
	if always && len(req.Suggestions) > 0 {
		resp["updatedPermissions"] = req.Suggestions
	}
	return c.respond(req.RequestID, resp)
}

func (c *Client) Deny(req *PermissionRequest, reason string) error {
	return c.respond(req.RequestID, map[string]any{"behavior": "deny", "message": reason})
}

func (c *Client) Close() {
	if c.closed.Swap(true) {
		return
	}
	c.stdin.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}

// control sends a control request. The tag prefixes the request ID so the
// matching ControlResult can be attributed (e.g. "mode-3").
func (c *Client) control(tag string, req map[string]any) error {
	if tag == "" {
		tag = "req"
	}
	id := fmt.Sprintf("%s-%d", tag, c.nextID.Add(1))
	return c.write(map[string]any{"type": "control_request", "request_id": id, "request": req})
}

func (c *Client) respond(requestID string, response any) error {
	return c.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		},
	})
}

func (c *Client) respondError(requestID, msg string) error {
	return c.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      msg,
		},
	})
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		for _, ev := range c.decode(sc.Bytes()) {
			c.events <- ev
		}
	}
	err := c.cmd.Wait()
	c.events <- Exited{Err: err, Stderr: strings.TrimSpace(c.stderr.String()), Requested: c.closed.Load()}
	close(c.events)
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
