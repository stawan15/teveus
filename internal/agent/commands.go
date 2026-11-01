package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/stawan15/teveus/internal/claude"
)

// Custom slash commands and skills are Markdown files. A skill is a directory
// with a SKILL.md, the format of the Agent Skills standard, so one written for
// any agent works here:
//
//	.teveus/skills/pdf/SKILL.md       /pdf   (also offered to the model, which loads it with the Skill tool)
//	.teveus/commands/review.md        /review  (a prompt; $ARGUMENTS, $1…$9 fill in what follows)
//	.teveus/commands/git/sync.md      /git:sync
//
// teveus has its own directories and reads them first: .teveus in each
// directory from the working directory up to the repository root (nearest
// wins), the config directory (skills that apply to every project), and
// ~/.config/teveus. The neutral .agents layout of the Agent Skills standard
// and the .claude layout are read after it, so files already written for
// other agents keep working. Like CLAUDE.md these are prompts, not programs:
// nothing in them runs by itself, and the model's tool calls still go through
// the permission checks. A file may start with front matter ("---", key:
// value lines, "---") holding description, argument-hint,
// disable-model-invocation and user-invocable.

type customCmd struct {
	name, description, hint, body string
	dir                           string // a skill's directory, so it can read files next to SKILL.md
	skill                         bool
	// modelRuns is false for disable-model-invocation: true, and listed is
	// false for user-invocable: false.
	modelRuns, listed bool
}

// loadCustom returns every custom command and skill visible from cwd, by name.
// configDir is teveus's own settings directory, whose skills apply everywhere.
func loadCustom(cwd, configDir string) []customCmd {
	seen := map[string]bool{}
	var out []customCmd
	add := func(c customCmd) {
		if c.name != "" && c.name != "compact" && !seen[c.name] {
			seen[c.name] = true
			out = append(out, c)
		}
	}
	var own, others []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		own = append(own, filepath.Join(dir, ".teveus"))
		others = append(others,
			filepath.Join(dir, ".agents"), filepath.Join(dir, ".claude"))
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir {
			break
		}
	}
	if configDir != "" {
		own = append(own, configDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		own = append(own, filepath.Join(home, ".config", "teveus"))
		others = append(others,
			filepath.Join(home, ".agents"), filepath.Join(home, ".claude"))
	}
	for _, root := range append(own, others...) {
		for _, c := range readCommandDir(filepath.Join(root, "commands")) {
			add(c)
		}
		for _, c := range readSkillDir(filepath.Join(root, "skills")) {
			add(c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func readCommandDir(dir string) []customCmd {
	var out []customCmd
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		name := strings.ReplaceAll(filepath.ToSlash(strings.TrimSuffix(rel, ".md")), "/", ":")
		if c, ok := readCustomFile(p, name, false); ok {
			out = append(out, c)
		}
		return nil
	})
	return out
}

func readSkillDir(dir string) []customCmd {
	entries, _ := os.ReadDir(dir)
	var out []customCmd
	for _, e := range entries {
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		if c, ok := readCustomFile(path, e.Name(), true); ok {
			c.dir = filepath.Dir(path)
			out = append(out, c)
		}
	}
	return out
}

func readCustomFile(path, name string, skill bool) (customCmd, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 200_000 {
		return customCmd{}, false
	}
	meta, body := splitFrontMatter(string(b))
	body = strings.TrimSpace(body)
	if body == "" {
		return customCmd{}, false
	}
	if skill && meta["name"] != "" {
		name = meta["name"]
	}
	desc := meta["description"]
	if desc == "" {
		desc, _, _ = strings.Cut(body, "\n")
		desc = strings.TrimSpace(strings.TrimLeft(desc, "# "))
	}
	if r := []rune(desc); len(r) > 100 {
		desc = string(r[:100]) + "…"
	}
	return customCmd{name: name, description: desc, hint: meta["argument-hint"], body: body,
		skill: skill, modelRuns: !truthy(meta["disable-model-invocation"]),
		listed: !falsey(meta["user-invocable"])}, true
}

// truthy and falsey read the boolean front-matter values the skill format
// allows, which are not just true and false.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

func falsey(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "no", "off", "0":
		return true
	}
	return false
}

// splitFrontMatter separates a leading "---" block of "key: value" lines.
func splitFrontMatter(s string) (map[string]string, string) {
	meta := map[string]string{}
	rest, ok := strings.CutPrefix(strings.ReplaceAll(s, "\r\n", "\n"), "---\n")
	if !ok {
		return meta, s
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return meta, s
	}
	for _, line := range strings.Split(head, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	_, body, _ = strings.Cut(body, "\n")
	return meta, body
}

var positional = regexp.MustCompile(`\$([1-9])`)

// expandCustom turns "/name args" into the command's prompt. It returns text
// unchanged when name isn't a custom command.
func expandCustom(cmds []customCmd, text string) string {
	name, args, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(text), "/"), " ")
	args = strings.TrimSpace(args)
	for _, c := range cmds {
		if c.name != name {
			continue
		}
		fields := strings.Fields(args)
		body := positional.ReplaceAllStringFunc(c.body, func(m string) string {
			if i := int(m[1] - '1'); i < len(fields) {
				return fields[i]
			}
			return ""
		})
		if strings.Contains(body, "$ARGUMENTS") {
			return strings.ReplaceAll(body, "$ARGUMENTS", args)
		}
		if args != "" && !positional.MatchString(c.body) {
			body += "\n\nARGUMENTS: " + args
		}
		return body
	}
	return text
}

// customCommands are the entries shown in the slash menu.
func customCommands(cmds []customCmd) []claude.Command {
	var out []claude.Command
	for _, c := range cmds {
		if !c.listed {
			continue // user-invocable: false hides it from the menu
		}
		out = append(out, claude.Command{Name: c.name, Description: c.description, ArgumentHint: c.hint})
	}
	return out
}

// skillsPrompt tells the model which skills exist; it loads one with the Skill tool.
func skillsPrompt(cmds []customCmd) string {
	var sb strings.Builder
	for _, c := range cmds {
		if c.skill && c.modelRuns {
			sb.WriteString("- " + c.name + ": " + c.description + "\n")
		}
	}
	if sb.Len() == 0 {
		return ""
	}
	return "\n# Skills\nWhen a task matches one of these, call the Skill tool with its name to load its instructions, then follow them:\n" + sb.String()
}

// hasSkills reports whether the model has a skill it could be offered.
func hasSkills(cmds []customCmd) bool {
	for _, c := range cmds {
		if c.skill && c.modelRuns {
			return true
		}
	}
	return false
}

// skillTool loads a skill by name. The directory is included in what the model
// sees, so a skill can point at the scripts and references bundled with it.
func skillTool(cwd, configDir string) tool {
	return tool{access: readOnly, def: ToolDef{Name: "Skill",
		Description: "Load a skill's instructions by name (see the Skills list in the system prompt).",
		Schema:      obj(map[string]any{"skill": prop("string", "The skill's name")}, "skill")},
		run: func(_ context.Context, _ *Toolbox, in map[string]any) (string, error) {
			name := strings.TrimPrefix(strings.TrimSpace(str(in, "skill")), "/")
			for _, c := range loadCustom(cwd, configDir) {
				if c.skill && c.modelRuns && c.name == name {
					if c.dir != "" {
						return "Directory: " + c.dir + "\n\n" + c.body, nil
					}
					return c.body, nil
				}
			}
			return "", errors.New("no skill named " + name)
		}}
}
