package antigravity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

// tailBuffer keeps the last bytes of the CLI's stderr, for the error message
// when it dies. The CLI is chatty on stderr while it works.
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

func timeoutAfter(d time.Duration) <-chan time.Time { return time.After(d) }

// rpcMessage is one line of the protocol, in either direction.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// notify sends a JSON-RPC 2.0 notification (no id, no reply expected).
func (c *Client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// request sends one call and waits for its answer (with a 60s timeout for
// setup/control calls).
func (c *Client) request(method string, params any) (json.RawMessage, error) {
	return c.requestWithTimeout(method, params, 60*time.Second)
}

// requestNoTimeout sends a call that may run for minutes (like session/prompt)
// and waits until the CLI replies or exits.
func (c *Client) requestNoTimeout(method string, params any) (json.RawMessage, error) {
	return c.requestWithTimeout(method, params, 0)
}

func (c *Client) requestWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	resp, err := c.callWithTimeout(id, method, params, timeout)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
	}
	return resp.Result, nil
}

// callWithTimeout writes the request and reads until the reply with that id
// arrives, the CLI exits, or timeout (if > 0) elapses.
func (c *Client) callWithTimeout(id int64, method string, params any, timeout time.Duration) (*rpcMessage, error) {
	ch := make(chan *rpcMessage, 1)
	c.mu.Lock()
	if c.waiting == nil {
		c.waiting = map[string]chan *rpcMessage{}
	}
	c.waiting[fmt.Sprint(id)] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiting, fmt.Sprint(id))
		c.mu.Unlock()
	}()

	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	var timer <-chan time.Time
	if timeout > 0 {
		timer = timeoutAfter(timeout)
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-c.done:
		return nil, fmt.Errorf("%s: CLI exited before replying", method)
	case <-timer:
		return nil, fmt.Errorf("%s: no answer from the CLI", method)
	}
}

// respond answers a request the CLI made.
func (c *Client) respond(id string, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (c *Client) emit(ev claude.Event) {
	if c.closed.Load() {
		return
	}
	select {
	case c.events <- ev:
	default:
		// The UI is behind; dropping would lose text, so wait a moment.
		select {
		case c.events <- ev:
		case <-timeoutAfter(5 * time.Second):
		}
	}
}

// read decodes the CLI's output for the life of the process.
func (c *Client) read(r io.Reader) {
	defer func() {
		if c.done != nil {
			close(c.done)
		}
		if !c.closed.Load() {
			c.emit(claude.Exited{Err: io.EOF, Stderr: c.stderr.String()})
		}
		close(c.events)
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			c.emit(claude.Unknown{Line: line})
			continue
		}
		switch {
		case msg.Method != "":
			c.handleNotification(msg)
		case len(msg.ID) > 0:
			c.deliver(msg)
		}
	}
}

// deliver hands a reply to whoever is waiting for it.
func (c *Client) deliver(msg rpcMessage) {
	key := strings.Trim(string(msg.ID), `"`)
	c.mu.Lock()
	ch, ok := c.waiting[key]
	c.mu.Unlock()
	if ok {
		select {
		case ch <- &msg:
		default:
		}
	}
}

// handleNotification turns one session/update into events the UI understands.
func (c *Client) handleNotification(msg rpcMessage) {
	switch msg.Method {
	case "session/request_permission":
		c.handlePermission(msg)
		return
	case "session/update":
	default:
		return // other notifications (telemetry, logs) are not ours
	}
	var p struct {
		Update struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       json.RawMessage `json:"content"`
			ToolCallID    string          `json:"toolCallId"`
			Name          string          `json:"name"`
			Status        string          `json:"status"`
			Kind          string          `json:"kind"`
			Title         string          `json:"title"`
			Args          json.RawMessage `json:"rawInput"`
		} `json:"update"`
	}
	if json.Unmarshal(msg.Params, &p) != nil {
		return
	}
	u := p.Update
	switch u.SessionUpdate {
	case "agent_message_chunk":
		var content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &content) == nil && content.Text != "" {
			c.mu.Lock()
			c.turnText.WriteString(content.Text)
			c.mu.Unlock()
			c.emit(claude.TextDelta{Text: content.Text})
		}

	case "agent_thought_chunk":
		var content struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &content) == nil && content.Text != "" {
			// The UI shows reasoning through AssistantMessage's thinking block.
			c.emit(claude.AssistantMessage{Blocks: []claude.ContentBlock{
				{Type: "thinking", Text: content.Text},
			}})
		}

	case "tool_call", "tool_call_update":
		name := mapToolName(u.Name)
		input := normalizeToolInput(name, u.Args)
		status := "running"
		switch u.Status {
		case "completed":
			status = "done"
		case "failed":
			status = "failed"
		}
		c.mu.Lock()
		if c.turnToolIdx != nil && u.ToolCallID != "" {
			if idx, ok := c.turnToolIdx[u.ToolCallID]; ok {
				if name != "" {
					c.turnTools[idx].Name = name
				}
				if len(input) > 2 {
					c.turnTools[idx].Input = input
				}
			} else {
				c.turnToolIdx[u.ToolCallID] = len(c.turnTools)
				c.turnTools = append(c.turnTools, agent.ToolCall{ID: u.ToolCallID, Name: name, Input: input})
			}
			if status != "running" && !c.turnResSeen[u.ToolCallID] {
				c.turnResSeen[u.ToolCallID] = true
				c.turnResults = append(c.turnResults, agent.Message{
					Role: "tool", ToolCallID: u.ToolCallID, Result: u.Title, IsError: status == "failed",
				})
			}
		}
		c.mu.Unlock()
		c.emit(claude.AssistantMessage{Blocks: []claude.ContentBlock{{
			Type: "tool_use", ID: u.ToolCallID, Name: name, Input: input,
		}}})
		if status != "running" {
			text := toolUpdateText(u.Content, u.Title)
			c.emit(claude.ToolResults{Results: []claude.ContentBlock{{
				Type: "tool_result", ToolUseID: u.ToolCallID, Text: text, IsError: status == "failed",
			}}})
		}

	case "usage_update":
		var usage struct {
			Used int `json:"used"`
			Size int `json:"size"`
		}
		if json.Unmarshal(msg.Params, &struct {
			Update *struct {
				Used int `json:"used"`
				Size int `json:"size"`
			} `json:"update"`
		}{&usage}) == nil && (usage.Used > 0 || usage.Size > 0) {
			c.emit(claude.ContextUsage{Used: usage.Used, Size: usage.Size})
		}
	}
}

// toolUpdateText extracts readable output from ACP tool_call_update content,
// falling back to title when content is empty.
func toolUpdateText(raw json.RawMessage, title string) string {
	if len(raw) == 0 {
		return title
	}
	var items []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &items) == nil {
		var parts []string
		for _, it := range items {
			if it.Content != nil && it.Content.Text != "" {
				parts = append(parts, it.Content.Text)
			} else if it.Text != "" {
				parts = append(parts, it.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	var single struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &single) == nil && single.Text != "" {
		return single.Text
	}
	return title
}

// handlePermission turns the CLI's question into the UI's, and answers it
// itself when the mode says so: prompt mode always asks, the others allow.
func (c *Client) handlePermission(msg rpcMessage) {
	var p struct {
		ToolCall struct {
			ToolCallID string          `json:"toolCallId"`
			Name       string          `json:"name"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(msg.Params, &p) != nil {
		return
	}
	tool := mapToolName(p.ToolCall.Name)
	id := strings.Trim(string(msg.ID), `"`)

	c.mu.Lock()
	always := c.always[tool]
	mode := c.mode
	c.mu.Unlock()

	if always || modeAllowsAll(mode) {
		c.respond(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}})
		return
	}

	req := &claude.PermissionRequest{
		RequestID:   id,
		ToolName:    tool,
		ToolUseID:   p.ToolCall.ToolCallID,
		Description: permissionDescription(p.ToolCall.Title, tool, p.ToolCall.RawInput),
		Input:       p.ToolCall.RawInput,
		AlwaysLabel: "allow " + tool + " for this session",
	}
	c.mu.Lock()
	c.perms[id] = req
	c.mu.Unlock()
	c.emit(req)
}

// modeAllowsAll reports whether the chosen permission mode answers without
// asking. "acceptEdits" still asks for commands, so it does not qualify.
func modeAllowsAll(mode string) bool {
	return mode == "auto" || mode == "bypassPermissions"
}

func permissionModeLabel(mode string) string {
	if mode == "" {
		return "default"
	}
	return mode
}
