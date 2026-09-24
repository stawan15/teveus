package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tools use Claude Code's names and input shapes so the transcript renderer
// (diffs, todo lists, command previews) works the same for every engine.

type access int

const (
	readOnly access = iota
	editsFiles
	runsCommands
	network // reads the web: allowed in plan mode, asked for otherwise
)

type tool struct {
	def    ToolDef
	access access
	run    func(ctx context.Context, t *Toolbox, in map[string]any) (string, error)
}

// Toolbox executes tools inside a working directory and remembers which
// files were read, so edits are always made against content the model saw.
type Toolbox struct {
	Cwd  string
	mu   sync.Mutex
	read map[string]bool

	jobMu  sync.Mutex // background commands (jobs.go)
	jobs   map[string]*job
	jobSeq int
}

func NewToolbox(cwd string) *Toolbox { return &Toolbox{Cwd: cwd, read: map[string]bool{}} }

func (t *Toolbox) abs(p string) string {
	if p == "" {
		return t.Cwd
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.Cwd, p)
	}
	return filepath.Clean(p)
}

func (t *Toolbox) markRead(p string) {
	t.mu.Lock()
	t.read[p] = true
	t.mu.Unlock()
}

func (t *Toolbox) wasRead(p string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.read[p]
}

func str(in map[string]any, k string) string { s, _ := in[k].(string); return s }

func num(in map[string]any, k string, def int) int {
	if f, ok := in[k].(float64); ok && f > 0 {
		return int(f)
	}
	return def
}

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

var tools = []tool{
	{access: readOnly, def: ToolDef{Name: "Read",
		Description: "Read a file (line-numbered). Use offset/limit for large files. Also lists a directory.",
		Schema: obj(map[string]any{
			"file_path": prop("string", "Path to the file (absolute or relative to the project)"),
			"offset":    prop("integer", "1-based line to start at"),
			"limit":     prop("integer", "Maximum lines to read (default 2000)"),
		}, "file_path")}, run: runRead},
	{access: editsFiles, def: ToolDef{Name: "Write",
		Description: "Create or overwrite a file with the given content. Read an existing file first.",
		Schema: obj(map[string]any{
			"file_path": prop("string", "Path to the file"),
			"content":   prop("string", "Full file content"),
		}, "file_path", "content")}, run: runWrite},
	{access: editsFiles, def: ToolDef{Name: "Edit",
		Description: "Replace exact text in a file. old_string must match exactly (including indentation) and be unique unless replace_all is true. Read the file first.",
		Schema: obj(map[string]any{
			"file_path":   prop("string", "Path to the file"),
			"old_string":  prop("string", "Exact text to replace"),
			"new_string":  prop("string", "Replacement text"),
			"replace_all": prop("boolean", "Replace every occurrence"),
		}, "file_path", "old_string", "new_string")}, run: runEdit},
	{access: runsCommands, def: ToolDef{Name: "Bash",
		Description: "Run a shell command in the project directory and return its output. Use for builds, tests, git and other CLI tools.",
		Schema: obj(map[string]any{
			"command":     prop("string", "The command to run"),
			"description": prop("string", "Five-word summary of what it does"),
			"timeout":     prop("integer", "Timeout in milliseconds (default 120000, max 600000)"),
		}, "command")}, run: runBash},
	{access: readOnly, def: ToolDef{Name: "Grep",
		Description: "Search file contents with a regular expression. Returns path:line: text.",
		Schema: obj(map[string]any{
			"pattern":          prop("string", "Regular expression"),
			"path":             prop("string", "File or directory to search (default: project)"),
			"glob":             prop("string", "Only files matching this glob, e.g. *.go"),
			"case_insensitive": prop("boolean", "Ignore case"),
		}, "pattern")}, run: runGrep},
	{access: readOnly, def: ToolDef{Name: "Glob",
		Description: "Find files by glob pattern such as **/*.ts or src/**/test_*.py, newest first.",
		Schema: obj(map[string]any{
			"pattern": prop("string", "Glob pattern"),
			"path":    prop("string", "Directory to search (default: project)"),
		}, "pattern")}, run: runGlob},
	{access: readOnly, def: ToolDef{Name: "TodoWrite",
		Description: "Maintain a task list for multi-step work. Send the full list each time; mark one item in_progress while working on it.",
		Schema: obj(map[string]any{
			"todos": map[string]any{"type": "array", "items": obj(map[string]any{
				"content": prop("string", "Task"),
				"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
			}, "content", "status")},
		}, "todos")}, run: func(context.Context, *Toolbox, map[string]any) (string, error) {
		return "Todo list updated.", nil
	}},
}

var webFetchTool = tool{access: network, def: ToolDef{Name: "WebFetch",
	Description: "Fetch a web page (http/https) and return its text. Use for documentation, issues or any URL the user gives.",
	Schema: obj(map[string]any{
		"url":    prop("string", "The URL to fetch"),
		"prompt": prop("string", "What you are looking for on the page"),
	}, "url")}, run: runWebFetch}

func findTool(list []tool, name string) (tool, bool) {
	for _, t := range list {
		if strings.EqualFold(t.def.Name, name) {
			return t, true
		}
	}
	return tool{}, false
}

func defsOf(list []tool) []ToolDef {
	var out []ToolDef
	for _, t := range list {
		out = append(out, t.def)
	}
	return out
}

func runRead(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	p := t.abs(str(in, "file_path"))
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(p)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			sb.WriteString(name + "\n")
		}
		return sb.String(), nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return fmt.Sprintf("(binary file, %d bytes)", len(b)), nil
	}
	t.markRead(p)
	text := string(b)
	if strings.HasSuffix(p, ".ipynb") {
		if nb, err := notebookText(b); err == nil {
			text = nb // the cells; a notebook that doesn't parse is shown as it is
		}
	}
	lines := strings.Split(text, "\n")
	start := num(in, "offset", 1) - 1
	limit := num(in, "limit", 2000)
	if start >= len(lines) {
		return fmt.Sprintf("(file has %d lines)", len(lines)), nil
	}
	end := min(start+limit, len(lines))
	var sb strings.Builder
	for i := start; i < end; i++ {
		l := lines[i]
		if len(l) > 2000 {
			l = l[:2000] + "…"
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", i+1, l)
	}
	if end < len(lines) {
		fmt.Fprintf(&sb, "… %d more lines (use offset=%d)\n", len(lines)-end, end+1)
	}
	return sb.String(), nil
}

func runWrite(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	p := t.abs(str(in, "file_path"))
	if _, err := os.Stat(p); err == nil && !t.wasRead(p) {
		return "", fmt.Errorf("%s exists: Read it before overwriting", p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	content := str(in, "content")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	t.markRead(p)
	return fmt.Sprintf("Wrote %d lines to %s", strings.Count(content, "\n")+1, p), nil
}

func runEdit(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	p := t.abs(str(in, "file_path"))
	old, nw := str(in, "old_string"), str(in, "new_string")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) && old == "" {
		return runWrite(context.Background(), t, map[string]any{"file_path": p, "content": nw})
	}
	if err != nil {
		return "", err
	}
	if !t.wasRead(p) {
		//lint:ignore ST1005 starts with the tool name
		return "", fmt.Errorf("Read %s before editing it", p)
	}
	if old == nw {
		return "", fmt.Errorf("old_string and new_string are identical")
	}
	s := string(b)
	n := strings.Count(s, old)
	switch {
	case old == "" || n == 0:
		return "", fmt.Errorf("old_string not found in %s (it must match exactly, including whitespace)", p)
	case n > 1 && in["replace_all"] != true:
		return "", fmt.Errorf("old_string occurs %d times; add surrounding context to make it unique or set replace_all", n)
	}
	if in["replace_all"] == true {
		s = strings.ReplaceAll(s, old, nw)
	} else {
		s = strings.Replace(s, old, nw, 1)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited %s", p), nil
}

const outputCap = 30000

func runBash(ctx context.Context, t *Toolbox, in map[string]any) (string, error) {
	if in["run_in_background"] == true {
		id := t.startJob(str(in, "command"))
		return "Started in the background as " + id + ". Read its output with BashOutput, stop it with KillShell.", nil
	}
	timeout := time.Duration(min(num(in, "timeout", 120000), 600000)) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCommand(ctx, str(in, "command"))
	cmd.Dir = t.Cwd
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := capOutput(buf.String())
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("timed out after %s\n%s", timeout, out)
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s\n(exit code %d)", out, ee.ExitCode())
		}
		return "", fmt.Errorf("%v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	return out, nil
}

func capOutput(s string) string {
	if len(s) <= outputCap {
		return s
	}
	half := outputCap / 2
	return s[:half] + fmt.Sprintf("\n… %d characters omitted …\n", len(s)-outputCap) + s[len(s)-half:]
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "vendor": true, "target": true, "dist": true, "build": true, "__pycache__": true}

func runGrep(ctx context.Context, t *Toolbox, in map[string]any) (string, error) {
	pattern, root := str(in, "pattern"), t.abs(str(in, "path"))
	ci := in["case_insensitive"] == true
	if rg, err := exec.LookPath("rg"); err == nil {
		args := []string{"-n", "--no-heading", "--color", "never", "-M", "300"}
		if ci {
			args = append(args, "-i")
		}
		if g := str(in, "glob"); g != "" {
			args = append(args, "-g", g)
		}
		args = append(args, "-e", pattern, root)
		out, _ := exec.CommandContext(ctx, rg, args...).Output()
		return grepResult(t, string(out)), nil
	}
	if ci {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	count := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || count >= 200 {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if g := str(in, "glob"); g != "" {
			if ok, _ := filepath.Match(g, d.Name()); !ok {
				return nil
			}
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if strings.IndexByte(line, 0) >= 0 {
				return nil // binary
			}
			if re.MatchString(line) {
				fmt.Fprintf(&sb, "%s:%d:%s\n", p, n, line)
				if count++; count >= 200 {
					break
				}
			}
		}
		return nil
	})
	return grepResult(t, sb.String()), nil
}

func grepResult(t *Toolbox, out string) string {
	out = strings.ReplaceAll(out, t.Cwd+string(filepath.Separator), "")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "No matches."
	}
	if len(lines) > 200 {
		lines = append(lines[:200], fmt.Sprintf("… %d more matches; narrow the pattern", len(lines)-200))
	}
	return capOutput(strings.Join(lines, "\n"))
}

func runGlob(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	root := t.abs(str(in, "path"))
	re, err := globRegexp(str(in, "pattern"))
	if err != nil {
		return "", err
	}
	type hit struct {
		path string
		mod  time.Time
	}
	var hits []hit
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if re.MatchString(filepath.ToSlash(rel)) {
			info, _ := d.Info()
			if info != nil {
				hits = append(hits, hit{rel, info.ModTime()})
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return "No files found.", nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })
	var sb strings.Builder
	for i, h := range hits {
		if i == 200 {
			fmt.Fprintf(&sb, "… %d more files", len(hits)-200)
			break
		}
		sb.WriteString(h.path + "\n")
	}
	return sb.String(), nil
}

// globRegexp supports *, ?, ** and {a,b}. A pattern without a slash matches
// the file name at any depth, like most tools do.
func globRegexp(g string) (*regexp.Regexp, error) {
	if !strings.Contains(g, "/") {
		g = "**/" + g
	}
	var sb strings.Builder
	sb.WriteString("^")
	braces := 0
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			if i+2 < len(g) && g[i+2] == '/' {
				sb.WriteString("(?:.*/)?")
				i += 2
			} else {
				sb.WriteString(".*")
				i++
			}
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		case c == '{':
			braces++
			sb.WriteString("(?:")
		case c == '}' && braces > 0:
			braces--
			sb.WriteString(")")
		case c == ',' && braces > 0:
			sb.WriteString("|")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

func parseInput(raw json.RawMessage) map[string]any {
	in := map[string]any{}
	json.Unmarshal(raw, &in)
	return in
}
