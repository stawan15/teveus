package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill's text links files beside it ("references/foo.md"), which are
// relative to the skill, not the project. The expansion has to say where the
// skill lives or the model looks in the wrong directory and reports them
// missing — the Skill tool already did this, /<skill> did not.
func TestSkillExpansionPointsAtItsDirectory(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents/skills/tech-me")
	os.MkdirAll(filepath.Join(skillDir, "references"), 0o755)
	os.WriteFile(filepath.Join(skillDir, "references/discovery.md"), []byte("q"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: tech-me\ndescription: advisor\n---\nSee [discovery](references/discovery.md)."), 0o644)

	out := expandCustom(loadCustom(dir, ""), "/tech-me review")
	if !strings.Contains(out, "Directory: "+skillDir) {
		t.Errorf("no directory line, so the link is unresolvable:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "ARGUMENTS: review") {
		t.Errorf("the arguments were dropped:\n%s", out)
	}

	// A plain command has no files beside it, so it gets no directory.
	os.MkdirAll(filepath.Join(dir, ".agents/commands"), 0o755)
	os.WriteFile(filepath.Join(dir, ".agents/commands/plan.md"), []byte("Plan it."), 0o644)
	if got := expandCustom(loadCustom(dir, ""), "/plan"); strings.Contains(got, "Directory:") {
		t.Errorf("a command got a skill directory: %q", got)
	}
}

// A skill that fills $ARGUMENTS inline still gets its directory: an early
// return on the substitution used to skip it.
func TestSkillExpansionWithArgumentsPlaceholder(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents/skills/x")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: x\ndescription: d\n---\nTopic: $ARGUMENTS"), 0o644)

	out := expandCustom(loadCustom(dir, ""), "/x scaling")
	if !strings.Contains(out, "Directory: "+skillDir) {
		t.Errorf("the $ARGUMENTS path skipped the directory:\n%s", out)
	}
	if !strings.Contains(out, "Topic: scaling") {
		t.Errorf("arguments not substituted:\n%s", out)
	}
}
