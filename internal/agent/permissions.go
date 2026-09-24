package agent

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Permission rules let the user allow or block tool calls for good, in
// Claude Code's syntax:
//
//	Bash               any command
//	Bash(npm test)     exactly this command
//	Bash(go test:*)    commands starting with "go test" (simple commands only)
//	WebFetch(domain:go.dev)
//	mcp__github        every tool of an MCP server; mcp__github__create_issue one tool
//	Edit, Write, WebSearch, …
//
// They live in <config>/permissions.json, globally and per project, so they
// never land in the user's repository:
//
//	{"allow": [...], "deny": [...], "projects": {"/path/to/repo": {"allow": [...], "deny": [...]}}}
//
// Deny wins over allow and applies in every mode; allow skips the prompt but
// never lifts plan mode.

type RuleSet struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type permFile struct {
	RuleSet
	Projects map[string]RuleSet `json:"projects,omitempty"`
}

type permissions struct {
	path, cwd string
	mu        sync.Mutex
}

func newPermissions(configDir, cwd string) *permissions {
	if configDir == "" {
		return &permissions{cwd: cwd}
	}
	return &permissions{path: filepath.Join(configDir, "permissions.json"), cwd: cwd}
}

func (p *permissions) load() permFile {
	var f permFile
	if p.path != "" {
		if b, err := os.ReadFile(p.path); err == nil {
			json.Unmarshal(b, &f)
		}
	}
	return f
}

// Rules returns the rules that apply here: global ones, then the project's.
func (p *permissions) Rules() RuleSet {
	if p == nil {
		return RuleSet{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	proj := f.Projects[p.cwd]
	return RuleSet{Allow: append(f.Allow, proj.Allow...), Deny: append(f.Deny, proj.Deny...)}
}

// check says whether rules decide this call: "deny", "allow" or "".
func (p *permissions) check(toolName string, in map[string]any) string {
	rs := p.Rules()
	for _, r := range rs.Deny {
		if ruleMatches(r, toolName, in) {
			return "deny"
		}
	}
	for _, r := range rs.Allow {
		if ruleMatches(r, toolName, in) {
			return "allow"
		}
	}
	return ""
}

// AddProjectRule saves an allow (or deny) rule for this project.
func (p *permissions) AddProjectRule(rule string, deny bool) error {
	if p == nil || p.path == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	if f.Projects == nil {
		f.Projects = map[string]RuleSet{}
	}
	rs := f.Projects[p.cwd]
	list := &rs.Allow
	if deny {
		list = &rs.Deny
	}
	for _, r := range *list {
		if r == rule {
			return nil
		}
	}
	*list = append(*list, rule)
	sort.Strings(*list)
	f.Projects[p.cwd] = rs
	return p.save(f)
}

// RemoveRule deletes a rule wherever it is (global or this project).
func (p *permissions) RemoveRule(rule string) error {
	if p == nil || p.path == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	drop := func(list []string) []string {
		var out []string
		for _, r := range list {
			if r != rule {
				out = append(out, r)
			}
		}
		return out
	}
	f.Allow, f.Deny = drop(f.Allow), drop(f.Deny)
	if rs, ok := f.Projects[p.cwd]; ok {
		rs.Allow, rs.Deny = drop(rs.Allow), drop(rs.Deny)
		if len(rs.Allow)+len(rs.Deny) == 0 {
			delete(f.Projects, p.cwd)
		} else {
			f.Projects[p.cwd] = rs
		}
	}
	return p.save(f)
}

func (p *permissions) save(f permFile) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func ruleMatches(rule, toolName string, in map[string]any) bool {
	rule = strings.TrimSpace(rule)
	name, arg, hasArg := strings.Cut(rule, "(")
	if hasArg {
		arg = strings.TrimSuffix(arg, ")")
	}
	if strings.HasPrefix(name, "mcp__") && !hasArg {
		// A server ("mcp__github") covers all its tools.
		return toolName == name || strings.HasPrefix(toolName, name+"__")
	}
	if !strings.EqualFold(name, toolName) {
		return false
	}
	if !hasArg || arg == "*" || arg == "" {
		return true
	}
	switch toolName {
	case "Bash":
		cmd := strings.TrimSpace(str(in, "command"))
		if prefix, ok := strings.CutSuffix(arg, ":*"); ok {
			// A prefix never covers a compound command: "go test && rm -rf ~"
			// must not ride on "go test:*".
			return simpleCommand(cmd) && (cmd == prefix || strings.HasPrefix(cmd, prefix+" "))
		}
		return cmd == arg
	case "WebFetch":
		if d, ok := strings.CutPrefix(arg, "domain:"); ok {
			u, err := url.Parse(str(in, "url"))
			return err == nil && (strings.EqualFold(u.Hostname(), d) || strings.HasSuffix(strings.ToLower(u.Hostname()), "."+strings.ToLower(d)))
		}
		return str(in, "url") == arg
	case "Read", "Edit", "Write", "NotebookEdit":
		ok, _ := filepath.Match(arg, editPath(in))
		return ok
	}
	return false
}

// simpleCommand reports whether cmd is one command without shell operators,
// substitutions or redirections.
func simpleCommand(cmd string) bool {
	return cmd != "" && !strings.ContainsAny(cmd, ";&|`$<>()\n\\") && !strings.Contains(cmd, "*")
}

// suggestRule is the rule "always allow" saves for a call, and how to say it.
// It returns "" when the call shouldn't be allowed for good (edits are
// allowed for the session instead).
func suggestRule(toolName string, in map[string]any) (rule, label string) {
	switch {
	case toolName == "Bash":
		cmd := strings.TrimSpace(str(in, "command"))
		if !simpleCommand(cmd) {
			return "Bash(" + cmd + ")", "always allow this exact command in this project"
		}
		f := strings.Fields(cmd)
		prefix := f[0]
		if len(f) > 1 && !strings.HasPrefix(f[1], "-") && !strings.ContainsAny(f[1], "/.=") {
			prefix += " " + f[1]
		}
		return "Bash(" + prefix + ":*)", "always allow `" + prefix + "` commands in this project"
	case toolName == "WebFetch":
		if u, err := url.Parse(str(in, "url")); err == nil && u.Hostname() != "" {
			return "WebFetch(domain:" + u.Hostname() + ")", "always allow fetching from " + u.Hostname()
		}
	case toolName == "WebSearch":
		return "WebSearch", "always allow web searches in this project"
	case strings.HasPrefix(toolName, "mcp__"):
		return toolName, "always allow this MCP tool in this project"
	}
	return "", ""
}

// PermissionRules returns the saved rules that apply to this project.
func (e *Engine) PermissionRules() RuleSet { return e.perm.Rules() }

// AddPermissionRule saves an allow or deny rule for this project.
func (e *Engine) AddPermissionRule(rule string, deny bool) error {
	return e.perm.AddProjectRule(rule, deny)
}

// RemovePermissionRule deletes a saved rule.
func (e *Engine) RemovePermissionRule(rule string) error { return e.perm.RemoveRule(rule) }
