# teveus

A friendly, fast terminal for AI coding: your Claude subscription through Claude Code, or any provider with your own keys.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/stawan15/teveus/main/install.sh | sh
```

macOS and Linux (Intel and Apple Silicon / ARM). It downloads the latest release, verifies its checksum and puts
`teveus` in `~/.local/bin`; no sudo needed. Run the same line again to update.
Options: `TEVEUS_INSTALL_DIR=/usr/local/bin`, `TEVEUS_VERSION=v0.1.0`.

Other ways: `go install github.com/stawan15/teveus@latest`, or download an archive (including Windows) from
[Releases](https://github.com/stawan15/teveus/releases).

For your Claude Pro/Max subscription, also install [Claude Code](https://claude.com/claude-code).
Other providers (OpenAI, Gemini, OpenRouter, Anthropic API, local models) need nothing else: use `/login`.

Uninstall: `rm ~/.local/bin/teveus` (and `rm -rf ~/.config/teveus` to remove settings and saved keys).

## Getting started

```sh
teveus          # in any project folder
```

The first run walks you through three choices (guidance level, look, how to connect), each skippable with esc.
Press `?` any time for every shortcut, `ctrl+k` for every action, `/settings` for every preference.

Made for a range of users:

- **Guided / Standard / Pro** guidance levels: starter prompts (press 1-4) and tips for newcomers, a compact view for experts.
- Plain-language permission modes: *ask first*, *auto-edit*, *plan only*, *autopilot*.
- Accessibility: a high-contrast theme, **reduce motion**, full keyboard use, and mouse support that can be turned off.
- Layout adapts from wide monitors down to narrow split panes.

A terminal UI for [Claude Code](https://claude.com/claude-code), built with Bubble Tea, Lip Gloss and Glamour.

It runs your installed `claude` CLI in stream-json mode, so you keep Claude Code's agent loop, tools,
`CLAUDE.md`, MCP servers, skills, hooks and login. This app only replaces the interface.

## Features

- **Command palette** (`ctrl+k`): every action and Claude command in one fuzzy-searchable list, grouped, with its shortcut shown
- **Pick, don't type**: `/effort`, `/model`, `/mode` and `/theme` open pickers instead of printing usage text
- **`@` file mentions** with fuzzy search over your project (git-aware)
- **Prompt history** with `↑`/`↓`, saved between sessions
- **5 themes** (claude, tokyo-night, catppuccin, gruvbox, light) that preview live as you move through the picker
- Streaming Markdown replies; slash-command output is set apart with a gutter and shows no cost line
- Tool cards with coloured diffs; **click a card** to expand it, or `ctrl+o` for all
- Permission prompt showing the real diff or command, with `y`/`a`/`n` buttons
- Permission mode shown on the input border; `shift+tab` cycles it
- Sidebar (`ctrl+b`) with cost, context, a plan progress bar, usage limits and recent activity
- Status bar hints change with what you're doing
- `ctrl+y` copies the last reply; desktop notification when a long task finishes or needs approval
- `/mouse` turns mouse capture off so you can select text; settings persist in `~/.config/teveus/`

## Two engines

- **Claude Code** (default): drives your installed `claude` CLI with your Claude login.
- **Direct API** (`-engine api`, or `/engine`): teveus's own agent calls providers directly with your keys.
  Nothing else to install.

Connect providers with `/login`:

| Provider | How |
|---|---|
| OpenRouter | **browser login** (OAuth, no copy-paste) or API key: hundreds of models |
| OpenAI, Anthropic, Google Gemini, Groq, DeepSeek, xAI, Mistral | API key (or the usual env var, e.g. `OPENAI_API_KEY`) |
| Ollama, LM Studio | local, no key |
| Custom | any OpenAI-compatible base URL + optional key |

Keys are checked against the provider before saving and stored in `~/.config/teveus/auth.json` (mode 600).
`/logout` removes them. `/model` then lists every model from every connected provider.

The API engine has its own tools (Read, Write, Edit, Bash, Grep, Glob, TodoWrite), the same approval prompts and
permission modes, plan mode, interrupt (kills the running command), `/compact`, prompt caching on Anthropic,
cost from OpenRouter (token counts elsewhere), and it follows your repo's `AGENTS.md` / `CLAUDE.md`.

## Uses fewer tokens than the normal CLI

Two savers are on by default (toggle with `/concise` and `/lean`, or run with `-full` to turn both off):

- **Concise answers**: an appended instruction for short, direct replies. On Opus this cut one typical
  question from 1,904 to 788 output tokens. Put your own wording in `stylePrompt` in `~/.config/teveus/settings.json`.
- **Lean tools**: 17 rarely used built-in tools are left out, so every request carries ~15% less input
  (measured: 40.9k → 34.7k tokens for a two-step task).

The sidebar turns the context size yellow past 100k tokens, and a one-time note suggests `/compact`,
because long conversations are what use up your limits fastest.

## Run

```sh
go build -o teveus . && ./teveus            # in any project directory
./teveus -model sonnet -mode acceptEdits
./teveus -c                                      # continue the last session here
./teveus -resume <session-id>
./teveus -theme tokyo-night
./teveus -engine api -model openai/gpt-5.4-mini
```

Mouse-wheel scrolling is on, so hold `option` (macOS) or `shift` to select text, or run `/mouse` to turn capture off.

## Layout

- `internal/claude`: starts the CLI process and speaks the stream-json protocol (user turns, `can_use_tool`
  permission requests, interrupt, `set_permission_mode`, `set_model`)
- `internal/ui`: the Bubble Tea model (`model.go`), layout and chrome (`view.go`), transcript rendering (`render.go`), theme (`styles.go`)

## Test

```sh
go test ./...                                                          # smoke test is skipped by default
TEVEUS_SMOKE=1 TEVEUS_DIR=$(mktemp -d) go test ./internal/ui -run Smoke -v   # drives a real session with haiku
```
