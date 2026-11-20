package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MCP servers from the command line, in the shape Claude Code uses:
//
//	teveus mcp add --transport http --scope user pastel https://host/api/mcp
//	teveus mcp add --scope project notes npx -y @modelcontextprotocol/server-filesystem ~/notes
//	teveus mcp list
//	teveus mcp remove --scope user pastel
//
// "user" is this machine's own mcp.json (no approval needed: it is the user's
// own choice). "project" is .mcp.json in the working directory, which a
// repository can ship, so the file is approved only when the user passes
// --approve. "local" is kept for Claude Code's spelling and means the same as
// user here, since teveus has no per-project private file.

// MCPAddOptions is one `mcp add` invocation.
type MCPAddOptions struct {
	Transport string            // "stdio" (default) or "http"
	Scope     string            // "user", "project" or "local"
	Name      string            // what its tools are grouped under
	Target    string            // the command line (stdio) or the URL (http)
	Env       map[string]string // picture the stdio server's environment
	Headers   map[string]string // picture the http server's headers, e.g. Authorization
	Approve   bool              // trust a project file as part of this call
	Cwd       string            // the project directory, for scope "project"
	ConfigDir string            // teveus's own settings directory
}

// MCPAdd writes one server into the chosen file and says where it went.
func MCPAdd(o MCPAddOptions) (string, error) {
	if o.ConfigDir == "" {
		return "", errors.New("no config directory")
	}
	if o.Name == "" {
		return "", errors.New("a server name is required")
	}
	if o.Cwd == "" {
		o.Cwd, _ = filepath.Abs(".")
	}
	cfg, err := mcpConfigFrom(o)
	if err != nil {
		return "", err
	}

	switch o.Scope {
	case "", "user", "local":
		cfgs, err := UserMCPConfig(o.ConfigDir)
		if err != nil {
			return "", err
		}
		cfgs[o.Name] = cfg
		if err := SaveUserMCPConfig(o.ConfigDir, cfgs); err != nil {
			return "", err
		}
		return fmt.Sprintf("added %s to %s", o.Name, filepath.Join(o.ConfigDir, "mcp.json")), nil

	case "project":
		path := filepath.Join(o.Cwd, ".mcp.json")
		cfgs := map[string]MCPConfig{}
		if existing, _, err := readMCPFile(path); err == nil {
			cfgs = existing
		} else if !os.IsNotExist(err) {
			return "", err
		}
		cfgs[o.Name] = cfg
		if err := writeMCPFile(path, cfgs); err != nil {
			return "", err
		}
		if o.Approve {
			m := newMCPManager(o.Cwd, o.ConfigDir)
			if err := m.trust(false); err != nil {
				return "", err
			}
			return fmt.Sprintf("added %s to %s and approved it", o.Name, path), nil
		}
		return fmt.Sprintf("added %s to %s; it starts once you approve the file (/mcp, or --approve)", o.Name, path), nil
	}
	return "", fmt.Errorf("unknown scope %q (use user or project)", o.Scope)
}

// mcpConfigFrom turns the command line into the config the file holds.
func mcpConfigFrom(o MCPAddOptions) (MCPConfig, error) {
	switch strings.ToLower(o.Transport) {
	case "", "stdio":
		fields := strings.Fields(strings.TrimSpace(o.Target))
		if len(fields) == 0 {
			return MCPConfig{}, errors.New("a command is required for a stdio server, e.g. npx -y some-server")
		}
		return MCPConfig{Command: fields[0], Args: fields[1:], Env: o.Env}, nil
	case "http", "streamable-http":
		url := strings.TrimSpace(o.Target)
		if url == "" {
			return MCPConfig{}, errors.New("a URL is required for an http server")
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return MCPConfig{}, fmt.Errorf("%q is not an http(s) URL", url)
		}
		return MCPConfig{Type: "http", URL: url, Headers: o.Headers}, nil
	}
	return MCPConfig{}, fmt.Errorf("unknown transport %q (use stdio or http)", o.Transport)
}

// MCPSetEnabled turns a server off or on from the command line. Off keeps the
// entry (and any token) but stops its tools being offered.
func MCPSetEnabled(name string, on bool, cwd, configDir string) error {
	if cwd == "" {
		cwd, _ = filepath.Abs(".")
	}
	// The user's file first, then the project's: whichever names it.
	if cfgs, err := UserMCPConfig(configDir); err == nil {
		if c, ok := cfgs[name]; ok {
			c.Disabled = !on
			cfgs[name] = c
			return SaveUserMCPConfig(configDir, cfgs)
		}
	}
	path := filepath.Join(cwd, ".mcp.json")
	cfgs, _, err := readMCPFile(path)
	if err != nil {
		return fmt.Errorf("no server named %s", name)
	}
	c, ok := cfgs[name]
	if !ok {
		return fmt.Errorf("no server named %s", name)
	}
	c.Disabled = !on
	cfgs[name] = c
	return writeMCPFile(path, cfgs)
}

// MCPRemove deletes a server from the file its scope names.
func MCPRemove(name, scope, cwd, configDir string) error {
	switch scope {
	case "", "user", "local":
		cfgs, err := UserMCPConfig(configDir)
		if err != nil {
			return err
		}
		if _, ok := cfgs[name]; !ok {
			return fmt.Errorf("no server named %s in %s", name, filepath.Join(configDir, "mcp.json"))
		}
		delete(cfgs, name)
		return SaveUserMCPConfig(configDir, cfgs)
	case "project":
		path := filepath.Join(cwd, ".mcp.json")
		cfgs, _, err := readMCPFile(path)
		if err != nil {
			return err
		}
		if _, ok := cfgs[name]; !ok {
			return fmt.Errorf("no server named %s in %s", name, path)
		}
		delete(cfgs, name)
		return writeMCPFile(path, cfgs)
	}
	return fmt.Errorf("unknown scope %q (use user or project)", scope)
}

// MCPList is what `mcp list` prints: every server in the user's file and the
// project's, what it runs, and whether the project's file is approved.
func MCPList(cwd, configDir string) []MCPEntry {
	var out []MCPEntry
	if cfgs, err := UserMCPConfig(configDir); err == nil {
		out = append(out, mcpEntries(cfgs, "user", true)...)
	}
	cfgs, raw, err := readMCPFile(filepath.Join(cwd, ".mcp.json"))
	if err == nil {
		m := newMCPManager(cwd, configDir)
		out = append(out, mcpEntries(cfgs, "project", m.trusted(raw))...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}

// MCPEntry is one row of `mcp list`.
type MCPEntry struct {
	Name, Scope, Transport, Target string
	Approved                       bool // project files only
	Disabled                       bool
}

func mcpEntries(cfgs map[string]MCPConfig, scope string, approved bool) []MCPEntry {
	var out []MCPEntry
	for name, c := range cfgs {
		transport := "stdio"
		if strings.EqualFold(c.Type, "http") || strings.EqualFold(c.Type, "streamable-http") {
			transport = "http"
		}
		out = append(out, MCPEntry{Name: name, Scope: scope, Transport: transport,
			Target: c.Describe(), Approved: approved, Disabled: c.Disabled})
	}
	return out
}

// writeMCPFile saves a .mcp.json. A new file is 0644 like any repository file;
// an existing one keeps its mode.
func writeMCPFile(path string, cfgs map[string]MCPConfig) error {
	b, err := json.MarshalIndent(mcpFile{MCPServers: cfgs}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(path, b, mode)
}
