package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MCP (Model Context Protocol) servers add tools to the Direct API engine.
// They are configured like Claude Code's: a project's .mcp.json and the
// user's own <config>/mcp.json, both {"mcpServers": {name: server}}.
//
// A project's .mcp.json comes with the repository and can start any program,
// so its servers only run once the user approves that exact file (/mcp).
// Editing the file withdraws the approval.

const mcpProtocol = "2025-06-18"

type MCPConfig struct {
	Type    string            `json:"type,omitempty"` // "stdio" (default) or "http"
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Describe is a one-line summary of what the server runs or connects to.
func (c MCPConfig) Describe() string {
	if c.URL != "" {
		return c.URL
	}
	return strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
}

type mcpFile struct {
	MCPServers map[string]MCPConfig `json:"mcpServers"`
}

func readMCPFile(path string) (map[string]MCPConfig, []byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var f mcpFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, b, fmt.Errorf("%s: %w", path, err)
	}
	return f.MCPServers, b, nil
}

// MCPServerStatus is what /mcp shows for one server.
type MCPServerStatus struct {
	Name, Scope, Target string
	State               string // "connected", "starting", "needs approval", or an error
	Tools               []string
}

// mcpManager owns the engine's MCP servers.
type mcpManager struct {
	cwd, configDir string

	mu      sync.Mutex
	servers map[string]*mcpServer
	pending map[string]MCPConfig // project servers awaiting approval
	errs    []string             // config files that couldn't be read
}

func newMCPManager(cwd, configDir string) *mcpManager {
	return &mcpManager{cwd: cwd, configDir: configDir, servers: map[string]*mcpServer{}, pending: map[string]MCPConfig{}}
}

func (m *mcpManager) projectFile() string { return filepath.Join(m.cwd, ".mcp.json") }

// start connects every configured server that may run (only the project's
// when user is false). It returns once all have connected or failed, so the
// first turn sees their tools.
func (m *mcpManager) start(user bool) {
	if m == nil {
		return
	}
	var wg sync.WaitGroup
	launch := func(name, scope string, cfg MCPConfig) {
		s := &mcpServer{name: name, scope: scope, cfg: cfg, cwd: m.cwd, state: "starting"}
		m.mu.Lock()
		if old := m.servers[name]; old != nil {
			old.close()
		}
		m.servers[name] = s
		m.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.connect()
		}()
	}
	if user && m.configDir != "" {
		if cfgs, _, err := readMCPFile(filepath.Join(m.configDir, "mcp.json")); err == nil {
			for name, c := range cfgs {
				launch(name, "user", c)
			}
		} else if !os.IsNotExist(err) {
			m.addErr(err.Error())
		}
	}
	cfgs, raw, err := readMCPFile(m.projectFile())
	switch {
	case err == nil && m.trusted(raw):
		for name, c := range cfgs {
			launch(name, "project", c)
		}
	case err == nil:
		m.mu.Lock()
		for name, c := range cfgs {
			if m.servers[name] == nil {
				m.pending[name] = c
			}
		}
		m.mu.Unlock()
	case !os.IsNotExist(err):
		m.addErr(err.Error())
	}
	wg.Wait()
}

func (m *mcpManager) addErr(s string) {
	m.mu.Lock()
	m.errs = append(m.errs, s)
	m.mu.Unlock()
}

// trust file: {project dir: sha256 of the approved .mcp.json}
func (m *mcpManager) trustPath() string { return filepath.Join(m.configDir, "mcp-approved.json") }

func (m *mcpManager) trusted(raw []byte) bool {
	if m.configDir == "" {
		return false
	}
	b, err := os.ReadFile(m.trustPath())
	if err != nil {
		return false
	}
	var t map[string]string
	json.Unmarshal(b, &t)
	sum := sha256.Sum256(raw)
	return t[m.cwd] == hex.EncodeToString(sum[:])
}

// approve records the current .mcp.json as approved and starts its servers.
func (m *mcpManager) approve() error {
	if m.configDir == "" {
		return errors.New("no config directory")
	}
	_, raw, err := readMCPFile(m.projectFile())
	if err != nil {
		return err
	}
	t := map[string]string{}
	if b, err := os.ReadFile(m.trustPath()); err == nil {
		json.Unmarshal(b, &t)
	}
	sum := sha256.Sum256(raw)
	t[m.cwd] = hex.EncodeToString(sum[:])
	b, _ := json.MarshalIndent(t, "", "  ")
	if err := os.MkdirAll(m.configDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(m.trustPath(), b, 0o600); err != nil {
		return err
	}
	m.mu.Lock()
	m.pending = map[string]MCPConfig{}
	m.mu.Unlock()
	m.start(false)
	return nil
}

func (m *mcpManager) status() []MCPServerStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MCPServerStatus
	for _, s := range m.servers {
		st := s.snapshot()
		out = append(out, st)
	}
	for name, c := range m.pending {
		out = append(out, MCPServerStatus{Name: name, Scope: "project", Target: c.Describe(), State: "needs approval"})
	}
	for _, e := range m.errs {
		out = append(out, MCPServerStatus{Name: "(config)", State: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *mcpManager) tools() []tool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	servers := make([]*mcpServer, 0, len(m.servers))
	for _, s := range m.servers {
		servers = append(servers, s)
	}
	m.mu.Unlock()
	sort.Slice(servers, func(i, j int) bool { return servers[i].name < servers[j].name })
	var out []tool
	for _, s := range servers {
		out = append(out, s.asTools()...)
	}
	return out
}

func (m *mcpManager) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		s.close()
	}
}

// mcpServer is one connected server.
type mcpServer struct {
	name, scope, cwd string
	cfg              MCPConfig

	mu    sync.Mutex
	state string
	tools []mcpTool
	conn  mcpConn
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations struct {
		ReadOnly bool `json:"readOnlyHint"`
	} `json:"annotations"`
}

func (s *mcpServer) snapshot() MCPServerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := MCPServerStatus{Name: s.name, Scope: s.scope, Target: s.cfg.Describe(), State: s.state}
	for _, t := range s.tools {
		st.Tools = append(st.Tools, t.Name)
	}
	return st
}

func (s *mcpServer) fail(err error) {
	s.mu.Lock()
	s.state = err.Error()
	s.mu.Unlock()
	s.close()
}

func (s *mcpServer) connect() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var conn mcpConn
	var err error
	switch strings.ToLower(s.cfg.Type) {
	case "", "stdio":
		if s.cfg.Command == "" {
			s.fail(errors.New("no command set"))
			return
		}
		conn, err = startStdio(s.cfg, s.cwd)
	case "http", "streamable-http":
		conn = &httpConn{url: expandEnv(s.cfg.URL), headers: expandMap(s.cfg.Headers)}
	default:
		err = fmt.Errorf("transport %q isn't supported (use stdio or http)", s.cfg.Type)
	}
	if err != nil {
		s.fail(err)
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := conn.call(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "teveus", "version": "1"},
	}, &init); err != nil {
		s.fail(fmt.Errorf("initialize: %w", err))
		return
	}
	conn.notify("notifications/initialized", nil)

	var all []mcpTool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []mcpTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := conn.call(ctx, "tools/list", params, &page); err != nil {
			s.fail(fmt.Errorf("tools/list: %w", err))
			return
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" || len(all) > 500 {
			break
		}
		cursor = page.NextCursor
	}
	s.mu.Lock()
	s.tools, s.state = all, "connected"
	s.mu.Unlock()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// toolName is how a server's tool is named to the model, as in Claude Code:
// mcp__server__tool, within the 64 characters providers allow.
func toolName(server, name string) string {
	n := "mcp__" + unsafeName.ReplaceAllString(server, "_") + "__" + unsafeName.ReplaceAllString(name, "_")
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

func (s *mcpServer) asTools() []tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "connected" {
		return nil
	}
	var out []tool
	for _, t := range s.tools {
		t := t
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		for k, v := range t.InputSchema {
			if k != "$schema" {
				schema[k] = v
			}
		}
		acc := runsCommands // tools can do anything: ask, as for a command
		if t.Annotations.ReadOnly {
			acc = readOnly
		}
		desc := t.Description
		if desc == "" {
			desc = t.Name
		}
		out = append(out, tool{access: acc,
			def: ToolDef{Name: toolName(s.name, t.Name), Description: fmt.Sprintf("[%s MCP server] %s", s.name, desc), Schema: schema},
			run: func(ctx context.Context, _ *Toolbox, in map[string]any) (string, error) {
				return s.callTool(ctx, t.Name, in)
			}})
	}
	return out
}

func (s *mcpServer) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return "", fmt.Errorf("MCP server %s isn't connected", s.name)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var res struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Resource struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := conn.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return "", err
	}
	var parts []string
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "resource":
			if c.Resource.Text != "" {
				parts = append(parts, c.Resource.Text)
			} else {
				parts = append(parts, "[resource "+c.Resource.URI+"]")
			}
		case "resource_link":
			parts = append(parts, "[resource link]")
		default:
			parts = append(parts, "["+c.Type+" "+c.MimeType+" not shown]")
		}
	}
	if len(parts) == 0 && len(res.Structured) > 0 {
		parts = append(parts, string(res.Structured))
	}
	out := capOutput(strings.Join(parts, "\n"))
	if res.IsError {
		return "", errors.New(out)
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	return out, nil
}

func (s *mcpServer) close() {
	s.mu.Lock()
	c := s.conn
	s.conn = nil
	s.mu.Unlock()
	if c != nil {
		c.close()
	}
}

func expandEnv(s string) string {
	return os.Expand(s, func(k string) string {
		name, def, hasDef := strings.Cut(k, ":-")
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		if hasDef {
			return def
		}
		return ""
	})
}

func expandMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = expandEnv(v)
	}
	return out
}

// mcpConn is a JSON-RPC 2.0 connection to a server.
type mcpConn interface {
	call(ctx context.Context, method string, params, result any) error
	notify(method string, params any)
	close()
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (m *rpcMessage) err() error {
	if m.Error != nil {
		return fmt.Errorf("%s (code %d)", m.Error.Message, m.Error.Code)
	}
	return nil
}

// stdioConn talks newline-delimited JSON over a child process's stdin/stdout.
type stdioConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	wmu    sync.Mutex
	mu     sync.Mutex
	nextID int
	wait   map[string]chan rpcMessage
	done   chan struct{}
	stderr *tailBuffer
}

func startStdio(cfg MCPConfig, cwd string) (*stdioConn, error) {
	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		args[i] = expandEnv(a)
	}
	cmd := exec.Command(expandEnv(cfg.Command), args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+expandEnv(v))
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &stdioConn{cmd: cmd, stdin: stdin, wait: map[string]chan rpcMessage{}, done: make(chan struct{}), stderr: &tailBuffer{max: 2000}}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go c.read(stdout)
	return c, nil
}

func (c *stdioConn) read(r io.Reader) {
	defer close(c.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var msg rpcMessage
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			c.answer(msg) // a request from the server
		case len(msg.ID) > 0:
			c.mu.Lock()
			ch := c.wait[string(msg.ID)]
			delete(c.wait, string(msg.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
	c.cmd.Wait()
}

// answer replies to server requests: ping works; everything else (sampling,
// roots, elicitation) isn't offered by this client.
func (c *stdioConn) answer(req rpcMessage) {
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if req.Method == "ping" {
		resp["result"] = map[string]any{}
	} else {
		resp["error"] = map[string]any{"code": -32601, "message": "method not supported by teveus"}
	}
	c.write(resp)
}

func (c *stdioConn) write(v any) error {
	b, _ := json.Marshal(v)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.stdin.Write(append(b, '\n'))
	return err
}

func (c *stdioConn) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.nextID++
	id := fmt.Sprint(c.nextID)
	ch := make(chan rpcMessage, 1)
	c.wait[id] = ch
	c.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params}); err != nil {
		return c.exitErr(err)
	}
	select {
	case msg := <-ch:
		if err := msg.err(); err != nil {
			return err
		}
		if result != nil {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	case <-c.done:
		return c.exitErr(errors.New("server exited"))
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
		c.notify("notifications/cancelled", map[string]any{"requestId": json.RawMessage(id), "reason": "cancelled by the user"})
		return ctx.Err()
	}
}

func (c *stdioConn) exitErr(err error) error {
	if s := strings.TrimSpace(c.stderr.String()); s != "" {
		return fmt.Errorf("%v: %s", err, s)
	}
	return err
}

func (c *stdioConn) notify(method string, params any) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	c.write(msg)
}

func (c *stdioConn) close() {
	c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		c.cmd.Process.Kill()
	}
}

// tailBuffer keeps the end of a stream (a server's stderr) for error messages.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// httpConn is the Streamable HTTP transport: each message is a POST, and the
// reply is JSON or a short SSE stream.
type httpConn struct {
	url     string
	headers map[string]string
	mu      sync.Mutex
	nextID  int
	session string
}

func (c *httpConn) post(ctx context.Context, body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocol)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	c.mu.Unlock()
	resp, err := httpDo(req)
	if err != nil {
		return nil, err
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.mu.Lock()
		c.session = s
		c.mu.Unlock()
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("%s: the server needs authorization; set its token in \"headers\" in the MCP config", resp.Status)
		}
		return nil, apiError(resp)
	}
	return resp, nil
}

func (c *httpConn) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.nextID++
	id := fmt.Sprint(c.nextID)
	c.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var msg rpcMessage
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		found := false
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
		var data strings.Builder
		for sc.Scan() && !found {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "data:"):
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case line == "" && data.Len() > 0:
				var m rpcMessage
				if json.Unmarshal([]byte(data.String()), &m) == nil && string(m.ID) == id && m.Method == "" {
					msg, found = m, true
				}
				data.Reset()
			}
		}
		if !found && data.Len() > 0 {
			if json.Unmarshal([]byte(data.String()), &msg) == nil && string(msg.ID) == id {
				found = true
			}
		}
		if !found {
			return errors.New("no reply in the event stream")
		}
	} else if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return err
	}
	if err := msg.err(); err != nil {
		return err
	}
	if result != nil {
		return json.Unmarshal(msg.Result, result)
	}
	return nil
}

func (c *httpConn) notify(method string, params any) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if resp, err := c.post(ctx, msg); err == nil {
		resp.Body.Close()
	}
}

func (c *httpConn) close() {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", session)
	if resp, err := httpDo(req); err == nil {
		resp.Body.Close()
	}
}

// Engine API for /mcp.

func (e *Engine) mcpTools() []tool { return e.mcp.tools() }

// MCPStatus lists the configured MCP servers.
func (e *Engine) MCPStatus() []MCPServerStatus { return e.mcp.status() }

// ApproveProjectMCP trusts this project's .mcp.json and starts its servers.
func (e *Engine) ApproveProjectMCP() error {
	if e.mcp == nil {
		return errors.New("MCP is off")
	}
	return e.mcp.approve()
}

// PendingProjectMCP lists a project's .mcp.json servers that haven't been
// approved, so the UI can say so before the engine starts them.
func PendingProjectMCP(cwd, configDir string) []string {
	m := newMCPManager(cwd, configDir)
	cfgs, raw, err := readMCPFile(m.projectFile())
	if err != nil || m.trusted(raw) {
		return nil
	}
	var names []string
	for n := range cfgs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
