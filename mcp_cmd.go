package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/stawan15/teveus/internal/agent"
)

// mcpCommand handles `teveus mcp …`, the terminal form of /mcp. It is the
// shape Claude Code uses, so the same command line works in either:
//
//	teveus mcp add --transport http --scope user pastel https://host/api/mcp
//	teveus mcp add --scope project notes npx -y some-server ~/notes
//	teveus mcp list
//	teveus mcp remove --scope user pastel
func mcpCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, mcpUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	cwd, _ := os.Getwd()
	cfgDir := configDir()

	switch verb {
	case "add":
		var (
			o       = agent.MCPAddOptions{Cwd: cwd, ConfigDir: cfgDir}
			command []string
		)
		// Flags may come before or after the name and its target.
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			value := func() string {
				if i+1 < len(rest) {
					i++
					return rest[i]
				}
				return ""
			}
			switch {
			case a == "--transport" || a == "-t":
				o.Transport = value()
			case strings.HasPrefix(a, "--transport="):
				o.Transport = strings.TrimPrefix(a, "--transport=")
			case a == "--scope" || a == "-s":
				o.Scope = value()
			case strings.HasPrefix(a, "--scope="):
				o.Scope = strings.TrimPrefix(a, "--scope=")
			case a == "--env" || a == "-e":
				k, v, ok := strings.Cut(value(), "=")
				if !ok {
					fmt.Fprintf(os.Stderr, "teveus mcp: --env wants KEY=value\n")
					return 2
				}
				if o.Env == nil {
					o.Env = map[string]string{}
				}
				o.Env[k] = v
			case a == "--header" || a == "-H":
				k, v, ok := strings.Cut(value(), ":")
				if !ok {
					fmt.Fprintf(os.Stderr, "teveus mcp: --header wants \"Name: value\"\n")
					return 2
				}
				if o.Headers == nil {
					o.Headers = map[string]string{}
				}
				o.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			case a == "--approve":
				o.Approve = true
			default:
				command = append(command, a)
			}
		}
		if len(command) == 0 {
			fmt.Fprintf(os.Stderr, "teveus mcp: add needs a name (and a command or a URL)\n")
			return 2
		}
		o.Name = command[0]
		// The rest is the command line for stdio and the URL for http; an URL
		// argument keeps its own word boundaries.
		o.Target = strings.Join(command[1:], " ")
		out, err := agent.MCPAdd(o)
		if err != nil {
			fmt.Fprintln(os.Stderr, "teveus mcp:", err)
			return 1
		}
		fmt.Println(out)

	case "remove", "rm":
		var (
			scope, name string
		)
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			switch {
			case a == "--scope" || a == "-s":
				if i+1 < len(rest) {
					i++
					scope = rest[i]
				}
			case strings.HasPrefix(a, "--scope="):
				scope = strings.TrimPrefix(a, "--scope=")
			default:
				name = a
			}
		}
		if name == "" {
			fmt.Fprintf(os.Stderr, "teveus mcp: remove needs a server name\n")
			return 2
		}
		if err := agent.MCPRemove(name, scope, cwd, cfgDir); err != nil {
			fmt.Fprintln(os.Stderr, "teveus mcp:", err)
			return 1
		}
		fmt.Println("removed", name)

	case "list", "ls":
		entries := agent.MCPList(cwd, cfgDir)
		if len(entries) == 0 {
			fmt.Println("No MCP servers configured.")
			fmt.Printf("Add one: teveus mcp add --transport http --scope user NAME URL\n")
			return 0
		}
		w := 0
		for _, e := range entries {
			w = max(w, len(e.Name))
		}
		for _, e := range entries {
			note := ""
			if e.Scope == "project" {
				if e.Approved {
					note = "  (approved)"
				} else {
					note = "  (needs approval: /mcp, or --approve on add)"
				}
			}
			fmt.Printf("%-*s  %-7s %-5s %s%s\n", w, e.Name, e.Scope, e.Transport, e.Target, note)
		}

	default:
		fmt.Fprint(os.Stderr, mcpUsage)
		return 2
	}
	return 0
}

const mcpUsage = `teveus mcp — MCP servers for the Direct API engine

  teveus mcp add [--transport stdio|http] [--scope user|project] [--env K=V] [--header "Name: value"] [--approve] NAME <command…|URL>
  teveus mcp list
  teveus mcp remove [--scope user|project] NAME

  user     your own file (~/.config/teveus/mcp.json), every project, no approval
  project  .mcp.json in this directory, shared with the repo, needs approval

Examples:
  teveus mcp add --transport http --scope user pastel https://host/api/mcp
  teveus mcp add --header "Authorization: Bearer TOKEN" --scope user pastel https://host/api/mcp
  teveus mcp add --scope project notes npx -y @modelcontextprotocol/server-filesystem ~/notes
`

// configDir mirrors the UI's location so the CLI writes the same file the app
// reads. TEVEUS_CONFIG wins, then ~/.config/teveus.
func configDir() string {
	if d := os.Getenv("TEVEUS_CONFIG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + string(os.PathSeparator) + ".config" + string(os.PathSeparator) + "teveus"
}
