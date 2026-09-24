package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const basePrompt = `You are an expert software engineer working as a coding agent in the user's terminal (teveus). You act through tools and report back briefly.

# How to work
- Investigate before changing anything: find relevant code with Glob and Grep, then Read it. Never guess at file contents, APIs or command output.
- Read a file before editing it. Prefer Edit for existing files; use Write for new files or full rewrites.
- Make the smallest change that fully solves the task, matching the surrounding code's style, naming and comment density.
- After changing code, verify it: run the build, tests or linter with Bash when the project has them, and fix what fails.
- For tasks with three or more steps, keep a TodoWrite list with exactly one item in_progress.
- Don't run destructive or irreversible commands (rm -rf, git reset --hard, git push --force, dropping data) unless the user explicitly asked.
- If the request is ambiguous in a way that changes the result, ask one short question instead of guessing.
- When a tool fails, read the error and adjust; don't repeat the same call unchanged.
- For broad searches across a codebase, or research that would fill your context, hand the work to a Task subagent; start several in one message when they're independent.
- Use WebFetch for documentation or any URL the user gives (and WebSearch when it's available) instead of guessing about libraries or APIs you're unsure of.
- Reference code as path:line.`

const planPrompt = `
# Plan mode is ON
Investigate with read-only tools (Read, Grep, Glob) and reply with a concise, concrete plan: the files to change and what changes. Do not modify files or run commands that change state; those calls will be refused.`

// systemPrompt assembles the prompt; call with e.mu held.
func (e *Engine) systemPrompt() string {
	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n# Environment\n")
	sb.WriteString("- Working directory: " + e.opts.Cwd + "\n")
	sb.WriteString("- Platform: " + runtime.GOOS + "\n")
	sb.WriteString("- Date: " + time.Now().Format("2006-01-02") + "\n")
	if e.model != "" {
		sb.WriteString("- Model: " + e.model + "\n")
	}
	if e.mode == "plan" {
		sb.WriteString(planPrompt + "\n")
	}
	if !e.opts.Attribution {
		sb.WriteString("\n# Git\n- Write commit messages and pull request descriptions as the user's own work: no Co-Authored-By trailers, no \"Generated with…\" lines, no mention of being an AI, unless the user asks for it.\n")
	}
	if e.opts.Style != "" {
		sb.WriteString("\n# Communication\n" + e.opts.Style + "\n")
	}
	if ins := projectInstructions(e.opts.Cwd); ins != "" {
		sb.WriteString("\n# Project instructions (from the repository; follow them)\n" + ins + "\n")
	}
	return sb.String()
}

// projectInstructions reads AGENTS.md / CLAUDE.md from the working directory
// up to the repository root, nearest last so it takes precedence.
func projectInstructions(cwd string) string {
	var found []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				found = append([]string{"## " + filepath.Join(dir, name) + "\n" + string(b)}, found...)
				break
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir {
			break
		}
	}
	s := strings.Join(found, "\n\n")
	if len(s) > 40000 {
		s = s[:40000] + "\n… (truncated)"
	}
	return s
}
