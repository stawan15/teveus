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

// Custom slash commands and skills are Markdown files. teveus reads its own
// ".agent" layout first, then Claude Code's ".claude" one, so files written
// for either keep working:
//
//	.agent/commands/review.md        /review      (a prompt; $ARGUMENTS, $1…$9 fill in what follows)
//	.agent/commands/git/sync.md      /git:sync
//	.agent/skills/pdf/SKILL.md       /pdf         (also offered to the model, which loads it with the Skill tool)
//
// Both layouts are read from the project (the working directory up to the
// repository root, nearest wins) and from the home directory. Like AGENTS.md
// they are prompts, not programs: nothing in them runs by itself, and the
// model's tool calls still go through the permission checks. A file may start
// with front matter ("---", key: value lines, "---") holding description and
// argument-hint.

type customCmd struct {
	name, description, hint, body string
	skill                         bool
}

// skillRoots are the directories commands and skills are read from, nearest
// first. own limits them to teveus's own ".agent" layout: those are the ones
// no other engine can see, so the UI expands them itself.
func skillRoots(cwd string, own bool) []string {
	layouts := []string{".agent"}
	if !own {
		layouts = append(layouts, ".claude")
	}
	var roots []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		for _, l := range layouts {
			roots = append(roots, filepath.Join(dir, l))
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir {
			break
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, l := range layouts {
			roots = append(roots, filepath.Join(home, l))
		}
	}
	return roots
}

// loadCustom returns every custom command and skill visible from cwd, by name.
func loadCustom(cwd string) []customCmd { return loadCustomIn(cwd, false) }

// ownCustom returns only the ones in teveus's own ".agent" layout.
func ownCustom(cwd string) []customCmd { return loadCustomIn(cwd, true) }

func loadCustomIn(cwd string, own bool) []customCmd {
	seen := map[string]bool{}
	var out []customCmd
	add := func(c customCmd) {
		if c.name != "" && c.name != "compact" && !seen[c.name] {
			seen[c.name] = true
			out = append(out, c)
		}
	}
	for _, root := range skillRoots(cwd, own) {
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
		if c, ok := readCustomFile(filepath.Join(dir, e.Name(), "SKILL.md"), e.Name(), true); ok {
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
	return customCmd{name: name, description: desc, hint: meta["argument-hint"], body: body, skill: skill}, true
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
		out = append(out, claude.Command{Name: c.name, Description: c.description, ArgumentHint: c.hint})
	}
	return out
}

// OwnCommands are the commands and skills in teveus's own ".agent" layout.
// Only this app reads that layout, so the UI offers them in the slash menu;
// the Direct API engine lists them itself.
func OwnCommands(cwd string) []claude.Command { return customCommands(ownCustom(cwd)) }

// ExpandOwn fills in a command or skill from teveus's own ".agent" layout, for
// an engine that can't read those files. It reports false when text isn't one.
func ExpandOwn(cwd, text string) (string, bool) {
	out := expandCustom(ownCustom(cwd), text)
	return out, out != text
}

// skillsPrompt tells the model which skills exist; it loads one with the Skill tool.
func skillsPrompt(cmds []customCmd) string {
	var sb strings.Builder
	for _, c := range cmds {
		if c.skill {
			sb.WriteString("- " + c.name + ": " + c.description + "\n")
		}
	}
	if sb.Len() == 0 {
		return ""
	}
	return "\n# Skills\nWhen a task matches one of these, call the Skill tool with its name to load its instructions, then follow them:\n" + sb.String()
}

func hasSkills(cmds []customCmd) bool {
	for _, c := range cmds {
		if c.skill {
			return true
		}
	}
	return false
}

func skillTool(cwd string) tool {
	return tool{access: readOnly, def: ToolDef{Name: "Skill",
		Description: "Load a skill's instructions by name (see the Skills list in the system prompt).",
		Schema:      obj(map[string]any{"skill": prop("string", "The skill's name")}, "skill")},
		run: func(_ context.Context, _ *Toolbox, in map[string]any) (string, error) {
			name := strings.TrimPrefix(strings.TrimSpace(str(in, "skill")), "/")
			for _, c := range loadCustom(cwd) {
				if c.skill && c.name == name {
					return c.body, nil
				}
			}
			return "", errors.New("no skill named " + name)
		}}
}
