package antigravity

import (
	"encoding/json"
	"strings"
)

// The CLI names its tools for itself; the transcript, the permission dialog
// and the diff/todo renderers all speak Claude Code's names. Mapping here keeps
// one vocabulary in the UI regardless of the engine.

// toolNames maps the CLI's tool to the name the UI renders.
var toolNames = map[string]string{
	// Files
	"view_file":                  "Read",
	"write_to_file":              "Write",
	"replace_file_content":       "Edit",
	"multi_replace_file_content": "Edit",
	"sed_file":                   "Edit",
	"notebook_edit":              "NotebookEdit",
	"find_by_name":               "Glob",
	"grep_search":                "Grep",
	"list_dir":                   "LS",

	// Commands
	"run_command":        "Bash",
	"send_command_input": "BashOutput",
	"command_status":     "BashOutput",

	// Web
	"read_url_content": "WebFetch",
	"search_web":       "WebSearch",

	// Planning and tasks
	"manage_task": "TodoWrite",
	"finish":      "TodoWrite",

	// Sub-agents
	"invoke_subagent":  "Task",
	"browser_subagent": "Task",
	"define_subagent":  "Task",
	"manage_subagents": "Task",

	// MCP
	"call_mcp_tool": "mcp__antigravity",
}

// mapToolName is the UI's name for one of the CLI's tools. An unknown tool keeps
// its own name rather than disappearing from the transcript.
func mapToolName(name string) string {
	if n, ok := toolNames[name]; ok {
		return n
	}
	return name
}

// paramNames turns the CLI's parameter spelling into Claude Code's, so the
// renderers show the path under a Read and the command under a Bash.
var paramNames = map[string]map[string]string{
	"Read": {
		"AbsolutePath": "file_path",
		"Path":         "file_path",
	},
	"Write": {
		"Path":    "file_path",
		"Content": "content",
	},
	"Edit": {
		"Path":               "file_path",
		"TargetContent":      "old_string",
		"ReplacementContent": "new_string",
		"ReplacementChunks":  "edits",
	},
	"Glob":      {"Pattern": "pattern", "SearchDirectory": "path"},
	"Grep":      {"Query": "pattern", "SearchPath": "path", "Includes": "glob"},
	"LS":        {"DirectoryPath": "path"},
	"Bash":      {"CommandLine": "command", "Cwd": "cwd", "WaitMsBeforeAsync": ""},
	"WebFetch":  {"Url": "url", "Prompt": "prompt"},
	"WebSearch": {"query": "query", "Query": "query"},
}

// normalizeToolInput rewrites the arguments the CLI sends into the shape the
// transcript renders. The permission dialog shows the original text too, so
// nothing is hidden from the user; this is only for the compact summary line.
func normalizeToolInput(uiName string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return raw
	}
	mapping, ok := paramNames[uiName]
	if !ok {
		return raw
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if to, ok := mapping[k]; ok {
			if to == "" {
				continue // a parameter the UI has no place for
			}
			out[to] = v
			continue
		}
		// Keep unknown keys: a renderer may still find them useful, and
		// nothing is lost for a tool we mapped by name only.
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return b
}

// permissionDescription is the one line the approval dialog shows above the
// arguments: what the tool is about to touch.
func permissionDescription(title, tool string, raw json.RawMessage) string {
	norm := normalizeToolInput(tool, raw)
	var in map[string]any
	json.Unmarshal(norm, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }

	switch tool {
	case "Bash":
		if c := str("command"); c != "" {
			return "run: " + firstLine(c)
		}
	case "Read", "Write", "Edit", "NotebookEdit":
		if p := str("file_path"); p != "" {
			return tool + " " + p
		}
	case "WebFetch":
		if u := str("url"); u != "" {
			return "fetch " + u
		}
	}
	if title != "" {
		return title
	}
	return "run " + tool
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	if len(s) > 100 {
		return s[:97] + "…"
	}
	return s
}
