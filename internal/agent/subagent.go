package agent

import (
	"context"
	"errors"
	"strings"
)

// tools lists what the model may call. The main conversation also gets Task;
// a subagent doesn't, so subagents never nest.
func (e *Engine) tools(main bool) []tool {
	list := append([]tool(nil), tools...)
	list = append(list, webFetchTool, notebookTool)
	if _, _, ok := searchKey(e.opts.Store); ok {
		list = append(list, tool{access: network, def: ToolDef{Name: "WebSearch",
			Description: "Search the web. Returns titles, URLs and snippets; WebFetch a result to read it.",
			Schema: obj(map[string]any{
				"query": prop("string", "The search query"),
			}, "query")},
			run: func(ctx context.Context, _ *Toolbox, in map[string]any) (string, error) {
				return e.runWebSearch(ctx, in)
			}})
	}
	if hasSkills(loadCustom(e.opts.Cwd)) {
		list = append(list, skillTool(e.opts.Cwd))
	}
	list = append(list, e.mcpTools()...)
	if main {
		list = append(list, tool{access: readOnly, def: ToolDef{Name: "Task",
			Description: "Start a subagent that researches on its own with read-only tools (Read, Grep, Glob, WebFetch, WebSearch) and returns a report. " +
				"Use it for broad searches across many files, or questions that would fill your context with file contents; give it a complete, self-contained prompt. " +
				"Several Task calls in one message run in parallel.",
			Schema: obj(map[string]any{
				"description": prop("string", "Three-to-five-word summary of the task"),
				"prompt":      prop("string", "The full task for the subagent, with everything it needs to know"),
			}, "description", "prompt")},
			run: nil}) // run by runTask: it needs the calling tool's ID
	}
	return list
}

// subagentSkips are read-only tools a research subagent still doesn't get:
// they change or depend on the main conversation's state.
var subagentSkips = map[string]bool{"TodoWrite": true, "Skill": true, "BashOutput": true, "KillShell": true}

const subagentPrompt = `You are a research subagent working for a coding agent in the user's terminal (teveus). You have read-only tools; you cannot edit files or run commands.

- Investigate the task thoroughly: find code with Glob and Grep, Read what matters, fetch web pages if the task needs them.
- Finish with a concise report that answers the task directly: the facts found, with file paths and line numbers (path:line) or URLs as evidence. Say what you could not find.
- The report is all the caller sees, so include every detail it will need, and nothing else.`

// subConv is a subagent's own conversation.
type subConv struct {
	e    *Engine
	hist []Message
}

func (c *subConv) system() string {
	var sb strings.Builder
	sb.WriteString(subagentPrompt)
	sb.WriteString("\n\n# Environment\n- Working directory: " + c.e.opts.Cwd + "\n")
	return sb.String()
}

func (c *subConv) messages() []Message { return append([]Message(nil), c.hist...) }
func (c *subConv) add(m Message)       { c.hist = append(c.hist, m) }

// runTask runs a subagent to completion and returns its final report. Its
// tool calls are emitted under the Task call's ID so the UI nests them.
func (e *Engine) runTask(ctx context.Context, id string, in map[string]any) (string, error) {
	prompt := strings.TrimSpace(str(in, "prompt"))
	if prompt == "" {
		return "", errors.New("prompt is empty")
	}
	client, modelID, err := e.client()
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	sub := e.opts.SubagentModel
	e.mu.Unlock()
	if sub != "" {
		// A model that can't be reached (key removed) falls back to the main one.
		if c, m, err := e.clientFor(sub); err == nil {
			client, modelID = c, m
		}
	}
	readOnlyTools := e.subagentTools()
	conv := &subConv{e: e, hist: []Message{{Role: "user", Text: prompt}}}
	if _, _, err := e.loop(ctx, client, modelID, conv, readOnlyTools, id); err != nil {
		return "", err
	}
	for i := len(conv.hist) - 1; i >= 0; i-- {
		if m := conv.hist[i]; m.Role == "assistant" && strings.TrimSpace(m.Text) != "" {
			return m.Text, nil
		}
	}
	return "", errors.New("the subagent finished without a report")
}

// subagentTools are the read-only tools a research subagent gets.
func (e *Engine) subagentTools() []tool {
	var out []tool
	for _, t := range e.tools(false) {
		if (t.access == readOnly || t.access == network) && !subagentSkips[t.def.Name] && !strings.HasPrefix(t.def.Name, "mcp__") {
			out = append(out, t)
		}
	}
	return out
}
