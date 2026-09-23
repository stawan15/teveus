# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

teveus is a Go terminal UI (Bubble Tea, Lip Gloss, Glamour) for Claude Code. It either drives the installed `claude` CLI in stream-json mode, or runs its own agent loop against provider APIs with the user's keys.

## Commands

```sh
go build -o teveus . && ./teveus        # run in any project dir; flags: -model -mode -c -resume -theme -engine api -full
go vet ./... && go test ./...                   # what CI runs (ubuntu + macos); live/smoke tests skip unless env vars are set
go test ./internal/agent -run TestPlanModeRefusesEdits -v   # single test
TEVEUS_SMOKE=1 TEVEUS_DIR=$(mktemp -d) go test ./internal/ui -run Smoke -v   # real Claude Code session (haiku)
LIVE_MODEL=google/gemini-3.5-flash go test ./internal/agent -run Live -v            # real provider, uses keys saved by /login
SNAP=1 go test ./internal/ui -run TestOnboardingFlow -v   # log rendered screens from UI tests
docs/record.sh                                  # re-record docs/demo.gif from a real session
python3 docs/bench/bench.py                     # benchmark against Claude Code and OpenCode (costs real tokens)
```

Releases are built by GoReleaser (`.goreleaser.yaml`, `release.yml`), which injects `main.version` via ldflags; `go install` builds fall back to the module version from build info.

## Architecture

**Two engines behind one interface.** `claude.Backend` (`internal/claude/backend.go`) is what the UI drives: `Events()`, `Send`, `Interrupt`, `SetPermissionMode`, `SetModel`, `Allow`/`Deny`, `Close`. Two implementations:
- `internal/claude.Client`: spawns the `claude` CLI and translates the stream-json protocol (user turns, `can_use_tool` control requests, interrupt, `set_permission_mode`, `set_model`) into typed events in `events.go`.
- `internal/agent.Engine`: teveus's own agent. `provider.go` defines providers and a `Client` interface with two wire protocols (`openai.go` for all OpenAI-compatible providers, `anthropic.go`). `tools.go` implements Read/Write/Edit/Bash/Grep/Glob/TodoWrite; `web.go` WebFetch and WebSearch (Brave/Tavily keys, listed in `SearchProviders`); `subagent.go` the Task tool (a read-only subagent whose events carry the Task call as `Parent`) and `Engine.tools()`, the per-request tool list; `mcp.go` MCP servers over stdio and Streamable HTTP (`.mcp.json` needs approval, recorded by file hash); `permissions.go` saved allow/deny rules in Claude Code syntax. `engine.go` runs the turn loop (`loop`, shared by the main conversation and subagents), permission asks, plan mode, `/compact` and auto-compaction, and undo. It emits the **same `claude.Event` types** as the CLI client, so the UI does not care which engine is running. Credentials live in `auth.go` (`~/.config/teveus/auth.json`), sessions in `session.go`.

When adding engine behaviour, keep both backends producing equivalent events. Engine-only features (e.g. `Undo`, `AddContext`, `Reload`) are reached by type-asserting the backend to `*agent.Engine`.

**UI (`internal/ui`).** A single Bubble Tea `Model` (`model.go`). `start()` picks the backend by `m.engine`. `listen()` reads one event per `tea.Cmd` and tags it with a generation counter `m.gen`, so events from a replaced backend (engine switch, `/clear`, resume) are dropped. `handleEvent` turns events into transcript `block`s, and `render.go` renders them with per-block caching (`invalidate()`).
- `commands.go`: `localCmds` are slash commands handled by the app. Anything not listed there goes to the CLI as-is. Pickers, the palette, and the `@` file popup share `popup.go`.
- `keys.go`: kitty keyboard protocol (shift+enter) decoding and long-paste folding. `images.go`: dropped/pasted image paths and ctrl+v clipboard images, scaled down before sending. `headless.go`: `teveus -p`. `scroller.go`: the transcript viewport (bubbles' viewport measured every line on each streamed chunk). `screen_test.go` checks no frame is taller or wider than the window.
- `saver.go`: the token savers. The concise-answer prompt and the minimal-code prompt (`minimal.go`, levels off/lite/full/strict, plus `/trim`) are appended as a system prompt, and `leanDisallowed` tools are passed to the CLI (Claude engine only). All are off until the user turns them on (`Concise`, `LeanTools`, `MinimalCode` in settings); `-full` forces them off. `slider.go` is the ← → level picker used for minimal code, guidance, `/effort` (`effort.go`: `--effort` for Claude Code; `output_config.effort` on Anthropic and `reasoning_effort` on OpenAI-style APIs, dropped and retried when a model rejects it) and CLI commands whose options form a scale. Anthropic thinking blocks are kept in `Message.Reasoning` and sent back unchanged.
- `styles.go`: themes. `view.go`: layout, sidebar, status bar. `selection.go`: mouse drag-select/copy. `login.go`: `/login` flows including OpenRouter OAuth.
- Settings persist in `~/.config/teveus/` (`configDir()` in `extras.go`); `TEVEUS_CONFIG` overrides it, and UI tests set it to `t.TempDir()` so they don't touch real settings.

**Anthropic's terms (keep teveus within them).** teveus is not affiliated with Anthropic, and its public release depends on these staying true (source: https://code.claude.com/docs/en/legal-and-compliance):
- Never read, store, forward or reuse Claude.ai credentials or OAuth tokens (e.g. `~/.claude/.credentials.json`, the macOS keychain entry), and never call Anthropic endpoints with them. Subscription use goes only through the unmodified `claude` binary, which signs in through Anthropic's own flow (`claude auth login`). Status comes from `claude auth status` and the CLI's own events.
- The Direct API engine uses API keys only (`x-api-key`). Don't add a "log in with Claude" flow to it, and don't send Claude Code's client identity headers.
- Pass the CLI only its documented flags; never patch the binary or disable any of its sign-in methods.
- Don't use Claude, Claude Code or Anthropic names or logos in teveus's own name, logo or feature names, or anywhere that suggests Anthropic built or endorses it. Plain descriptions like "runs Claude Code" are fine.
- Don't copy Claude Code's prompts, text or artwork.

**Privacy and safety (keep PRIVACY.md and SECURITY.md true).** teveus sends nothing on its own: no telemetry, analytics or update checks. Any change that makes a new network request, stores new data, or changes a safeguard must update PRIVACY.md / SECURITY.md in the same change. File tools go through `Engine.sensitive` (credential stores, blocked in every mode) and `Engine.inProject` (outside the project always asks, except autopilot); new file tools must be added to `toolPaths`. Files in the config dir are written with `writePrivate` (0600).

**Testing patterns.** Agent tests use an `httptest` server that returns scripted SSE replies (`script` in `internal/agent/engine_test.go`). UI tests build a `Model` directly and feed it `tea.Msg`s (see `api_test.go`, `selection_test.go`).
