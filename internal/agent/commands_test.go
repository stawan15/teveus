package agent

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExpandCustom(t *testing.T) {
	cmds := []customCmd{
		{name: "fix", body: "Fix issue $1 in $2, then $ARGUMENTS."},
		{name: "review", body: "Review the diff."},
	}
	for _, c := range []struct{ in, want string }{
		{"/fix 12 main.go", "Fix issue 12 in main.go, then 12 main.go."},
		{"/review focus on tests", "Review the diff.\n\nARGUMENTS: focus on tests"},
		{"/review", "Review the diff."},
		{"/unknown x", "/unknown x"},
	} {
		if got := expandCustom(cmds, c.in); got != c.want {
			t.Errorf("expandCustom(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLoadCustom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	sub := filepath.Join(root, "pkg")
	writeFile(t, filepath.Join(root, ".claude/commands/review.md"), "---\ndescription: \"Review it\"\nargument-hint: [file]\n---\n\nLook at $ARGUMENTS.\n")
	writeFile(t, filepath.Join(root, ".claude/commands/git/sync.md"), "# Sync branches\nRun git fetch.")
	writeFile(t, filepath.Join(root, ".claude/skills/pdf/SKILL.md"), "---\nname: pdf\ndescription: Work with PDFs\n---\nUse pdftotext.")
	writeFile(t, filepath.Join(root, ".claude/commands/empty.md"), "---\ndescription: x\n---\n")
	writeFile(t, filepath.Join(root, ".claude/commands/compact.md"), "must not shadow the built-in")

	got := loadCustom(sub) // found from a subdirectory, like CLAUDE.md
	names := map[string]customCmd{}
	for _, c := range got {
		names[c.name] = c
	}
	if len(names) != 3 {
		t.Fatalf("commands = %v", got)
	}
	if c := names["review"]; c.description != "Review it" || c.hint != "[file]" || c.body != "Look at $ARGUMENTS." {
		t.Errorf("review = %+v", c)
	}
	if c := names["git:sync"]; c.description != "Sync branches" {
		t.Errorf("git:sync = %+v", c)
	}
	if c := names["pdf"]; !c.skill || !strings.Contains(skillsPrompt(got), "- pdf: Work with PDFs") {
		t.Errorf("pdf = %+v", c)
	}
}

func TestCustomCommandReachesModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &script{replies: []string{oaiText("ok")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, dir := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	writeFile(t, filepath.Join(dir, ".claude/commands/greet.md"), "Say hello to $ARGUMENTS.")
	r.e.Send("/greet Ada")
	r.until(true)

	if !strings.Contains(fmt.Sprint(s.requests[0]["messages"]), "Say hello to Ada.") {
		t.Fatalf("the model did not get the expanded prompt: %v", s.requests[0]["messages"])
	}
}

func TestSkillTool(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude/skills/pdf/SKILL.md"), "---\ndescription: PDFs\n---\nUse pdftotext.")
	out, err := skillTool(dir).run(context.TODO(), nil, map[string]any{"skill": "pdf"})
	if err != nil || out != "Use pdftotext." {
		t.Fatalf("Skill = %q, %v", out, err)
	}
	if _, err := skillTool(dir).run(context.TODO(), nil, map[string]any{"skill": "nope"}); err == nil {
		t.Fatal("an unknown skill should be an error")
	}
}
