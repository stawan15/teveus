package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command line Claude Code uses has to work the same here.
func TestMCPAddHTTPUserScope(t *testing.T) {
	cfg := t.TempDir()
	out, err := MCPAdd(MCPAddOptions{
		Transport: "http", Scope: "user", Name: "pastel",
		Target: "https://banana.bananabreaking.com/api/mcp",
		Cwd:    t.TempDir(), ConfigDir: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mcp.json") {
		t.Errorf("output = %q", out)
	}
	cfgs, err := UserMCPConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := cfgs["pastel"]
	if c.Type != "http" || c.URL != "https://banana.bananabreaking.com/api/mcp" {
		t.Fatalf("saved %+v", c)
	}
}

// A stdio server keeps the command and each argument as its own word, and the
// environment it was given.
func TestMCPAddStdioKeepsArguments(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	out, err := MCPAdd(MCPAddOptions{
		Scope: "user", Name: "notes",
		Target: "npx -y @modelcontextprotocol/server-filesystem ~/notes",
		Env:    map[string]string{"HOME_DIR": "/tmp/x"},
		Cwd:    dir, ConfigDir: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mcp.json") {
		t.Errorf("output = %q", out)
	}
	cfgs, _ := UserMCPConfig(cfg)
	c := cfgs["notes"]
	if c.Command != "npx" {
		t.Errorf("command = %q", c.Command)
	}
	if got := strings.Join(c.Args, "|"); got != "-y|@modelcontextprotocol/server-filesystem|~/notes" {
		t.Errorf("args = %q", got)
	}
	if c.Env["HOME_DIR"] != "/tmp/x" {
		t.Errorf("env = %v", c.Env)
	}
}

// A project server needs approval, and asking for it in the same call is what
// --approve does.
func TestMCPAddProjectScopeAndApproval(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	out, err := MCPAdd(MCPAddOptions{Scope: "project", Name: "a", Target: "npx one", Cwd: dir, ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "approve") {
		t.Errorf("did not say it needs approval: %q", out)
	}
	for _, e := range MCPList(dir, cfg) {
		if e.Name == "a" && e.Approved {
			t.Fatal("unapproved file reported as approved")
		}
	}

	if _, err := MCPAdd(MCPAddOptions{Scope: "project", Name: "b", Target: "npx two", Approve: true, Cwd: dir, ConfigDir: cfg}); err != nil {
		t.Fatal(err)
	}
	for _, e := range MCPList(dir, cfg) {
		if e.Scope == "project" && !e.Approved {
			t.Errorf("%s is not approved after --approve", e.Name)
		}
	}

	// Adding to an approved file withdraws the approval: the hash changed.
	if _, err := MCPAdd(MCPAddOptions{Scope: "project", Name: "c", Target: "npx three", Cwd: dir, ConfigDir: cfg}); err != nil {
		t.Fatal(err)
	}
	for _, e := range MCPList(dir, cfg) {
		if e.Scope == "project" && e.Approved {
			t.Errorf("%s stayed approved after the file changed", e.Name)
		}
	}
}

// A bad command line is refused, not half-written.
func TestMCPAddRefusesBadInput(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	for _, o := range []MCPAddOptions{
		{Name: "", Target: "npx x"},
		{Transport: "http", Name: "no-url"},
		{Transport: "http", Name: "not-a-url", Target: "banana"},
		{Transport: "carrier-pigeon", Name: "x", Target: "y"},
		{Transport: "stdio", Name: "empty"},
		{Name: "bad-scope", Target: "npx x", Scope: "galaxy"},
	} {
		o.ConfigDir, o.Cwd = cfg, dir
		if _, err := MCPAdd(o); err == nil {
			t.Errorf("accepted %+v", o)
		}
	}
	if cfgs, _ := UserMCPConfig(cfg); len(cfgs) != 0 {
		t.Errorf("a refused call wrote something: %+v", cfgs)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Error("a refused project call created .mcp.json")
	}
}

// remove takes the server out of the file its scope names, and only that file.
func TestMCPRemoveScopes(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	MCPAdd(MCPAddOptions{Scope: "user", Name: "u", Target: "npx u", Cwd: dir, ConfigDir: cfg})
	MCPAdd(MCPAddOptions{Scope: "project", Name: "p", Target: "npx p", Cwd: dir, ConfigDir: cfg})

	if err := MCPRemove("p", "user", dir, cfg); err == nil {
		t.Error("removed a project server through the user scope")
	}
	if err := MCPRemove("u", "project", dir, cfg); err == nil {
		t.Error("removed a user server through the project scope")
	}
	if err := MCPRemove("u", "user", dir, cfg); err != nil {
		t.Fatal(err)
	}
	if cfgs, _ := UserMCPConfig(cfg); len(cfgs) != 0 {
		t.Errorf("user file still has %+v", cfgs)
	}
	if cfgs, _, _ := readMCPFile(filepath.Join(dir, ".mcp.json")); len(cfgs) != 1 {
		t.Errorf("the project file was touched: %+v", cfgs)
	}
}

// list shows both scopes with the transport the file implies.
func TestMCPListBothScopes(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	MCPAdd(MCPAddOptions{Transport: "http", Scope: "user", Name: "web", Target: "https://x.example/mcp", Cwd: dir, ConfigDir: cfg})
	MCPAdd(MCPAddOptions{Scope: "project", Name: "files", Target: "npx -y fs", Cwd: dir, ConfigDir: cfg})

	got := MCPList(dir, cfg)
	if len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	if got[0].Name != "files" || got[0].Scope != "project" || got[0].Transport != "stdio" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Name != "web" || got[1].Scope != "user" || got[1].Transport != "http" {
		t.Errorf("second = %+v", got[1])
	}
	if got[1].Target != "https://x.example/mcp" {
		t.Errorf("target = %q", got[1].Target)
	}
}

// Adding one server never drops the others.
func TestMCPAddKeepsExisting(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	for _, n := range []string{"one", "two", "three"} {
		if _, err := MCPAdd(MCPAddOptions{Scope: "user", Name: n, Target: "npx " + n, Cwd: dir, ConfigDir: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	if cfgs, _ := UserMCPConfig(cfg); len(cfgs) != 3 {
		t.Errorf("lost servers: %+v", cfgs)
	}
}
