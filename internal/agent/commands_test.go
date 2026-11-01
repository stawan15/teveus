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

func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

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
	isolateHome(t)
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	sub := filepath.Join(root, "pkg")
	writeFile(t, filepath.Join(root, ".claude/commands/review.md"), "---\ndescription: \"Review it\"\nargument-hint: [file]\n---\n\nLook at $ARGUMENTS.\n")
	writeFile(t, filepath.Join(root, ".claude/commands/git/sync.md"), "# Sync branches\nRun git fetch.")
	writeFile(t, filepath.Join(root, ".claude/skills/pdf/SKILL.md"), "---\nname: pdf\ndescription: Work with PDFs\n---\nUse pdftotext.")
	writeFile(t, filepath.Join(root, ".claude/commands/empty.md"), "---\ndescription: x\n---\n")
	writeFile(t, filepath.Join(root, ".claude/commands/compact.md"), "must not shadow the built-in")

	got := loadCustom(sub, "") // found from a subdirectory, like CLAUDE.md
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
	if c := names["pdf"]; !c.skill || !c.modelRuns || !c.listed || c.dir != filepath.Join(root, ".claude/skills/pdf") {
		t.Errorf("pdf = %+v", c)
	}
	if !strings.Contains(skillsPrompt(got), "- pdf: Work with PDFs") {
		t.Errorf("pdf is not offered to the model: %q", skillsPrompt(got))
	}
}

// teveus reads its own .teveus directories first, then the neutral .agents
// layout of the Agent Skills standard, then Claude Code's .claude.
func TestCustomDirsInOrder(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	cfg := t.TempDir()
	for _, dir := range []string{".teveus", ".agents", ".claude"} {
		writeFile(t, filepath.Join(root, dir, "commands", "same.md"), "# "+dir)
	}
	writeFile(t, filepath.Join(root, ".agents", "skills", "neutral", "SKILL.md"), "---\ndescription: Neutral\n---\nDo it.")
	writeFile(t, filepath.Join(cfg, "skills", "global", "SKILL.md"), "---\ndescription: Everywhere\n---\nDo it.")

	names := map[string]customCmd{}
	for _, c := range loadCustom(root, cfg) {
		names[c.name] = c
	}
	if got := names["same"].body; got != "# .teveus" {
		t.Errorf("the nearest root did not win: %q", got)
	}
	if c := names["neutral"]; !c.skill || c.dir != filepath.Join(root, ".agents", "skills", "neutral") {
		t.Errorf("neutral = %+v", c)
	}
	if c, ok := names["global"]; !ok || c.dir != filepath.Join(cfg, "skills", "global") {
		t.Errorf("a skill in the config directory does not apply here: %+v", c)
	}
}

// A skill that says not to run on its own must not be offered to the model,
// and one that says the user cannot invoke it must stay out of the menu.
func TestSkillInvocationFlags(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	cfg := t.TempDir()
	writeFile(t, filepath.Join(cfg, "skills", "manual", "SKILL.md"),
		"---\ndescription: Only when asked\ndisable-model-invocation: true\n---\nDo it.")
	writeFile(t, filepath.Join(cfg, "skills", "hidden", "SKILL.md"),
		"---\ndescription: Model only\nuser-invocable: false\n---\nDo it.")

	cmds := loadCustom(root, cfg)
	if p := skillsPrompt(cmds); strings.Contains(p, "manual") || !strings.Contains(p, "hidden") {
		t.Errorf("skills offered to the model: %q", p)
	}
	var inMenu []string
	for _, c := range customCommands(cmds) {
		inMenu = append(inMenu, c.Name)
	}
	for _, n := range inMenu {
		if n == "hidden" {
			t.Error("user-invocable: false appeared in the slash menu")
		}
		if n == "manual" {
			return // the user can still run it
		}
	}
	t.Error("a skill the user can invoke is missing from the menu")
}

func TestCustomCommandReachesModel(t *testing.T) {
	isolateHome(t)
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
	isolateHome(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude/skills/pdf/SKILL.md"), "---\ndescription: PDFs\n---\nUse pdftotext.")
	out, err := skillTool(dir, "").run(context.TODO(), nil, map[string]any{"skill": "pdf"})
	// The directory comes with the instructions, so a skill can point at the
	// scripts bundled next to its SKILL.md.
	if want := "Directory: " + filepath.Join(dir, ".claude/skills/pdf"); err != nil || !strings.HasPrefix(out, want+"\n\n") || !strings.HasSuffix(out, "Use pdftotext.") {
		t.Fatalf("Skill = %q, %v", out, err)
	}
	if _, err := skillTool(dir, "").run(context.TODO(), nil, map[string]any{"skill": "nope"}); err == nil {
		t.Fatal("an unknown skill should be an error")
	}
}

// The Skill tool is where disable-model-invocation has to hold: listing the
// skill in the prompt is not enough if the model can call it anyway.
func TestSkillToolRefusesModelDisabledSkill(t *testing.T) {
	isolateHome(t)
	cfg := t.TempDir()
	writeFile(t, filepath.Join(cfg, "skills", "manual", "SKILL.md"),
		"---\ndescription: Only when asked\ndisable-model-invocation: true\n---\nDo it.")
	if _, err := skillTool(t.TempDir(), cfg).run(context.TODO(), nil, map[string]any{"skill": "manual"}); err == nil {
		t.Fatal("a skill set to not run on its own was loaded by the model")
	}
}
