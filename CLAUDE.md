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
```

Releases are built by GoReleaser (`.goreleaser.yaml`, `release.yml`), which injects `main.version` via ldflags; `go install` builds fall back to the module version from build info.

## Architecture

**Two engines behind one interface.** `claude.Backend` (`internal/claude/backend.go`) is what the UI drives: `Events()`, `Send`, `Interrupt`, `SetPermissionMode`, `SetModel`, `Allow`/`Deny`, `Close`. Two implementations:
- `internal/claude.Client`: spawns the `claude` CLI and translates the stream-json protocol (user turns, `can_use_tool` control requests, interrupt, `set_permission_mode`, `set_model`) into typed events in `events.go`.
- `internal/agent.Engine`: teveus's own agent. `provider.go` defines providers and a `Client` interface with two wire protocols (`openai.go` for all OpenAI-compatible providers, `anthropic.go`). `tools.go` implements Read/Write/Edit/Bash/Grep/Glob/TodoWrite; `engine.go` runs the turn loop, permission asks, plan mode, `/compact`, and undo. It emits the **same `claude.Event` types** as the CLI client, so the UI does not care which engine is running. Credentials live in `auth.go` (`~/.config/teveus/auth.json`), sessions in `session.go`.

When adding engine behaviour, keep both backends producing equivalent events. Engine-only features (e.g. `Undo`, `AddContext`, `Reload`) are reached by type-asserting the backend to `*agent.Engine`.

**UI (`internal/ui`).** A single Bubble Tea `Model` (`model.go`). `start()` picks the backend by `m.engine`. `listen()` reads one event per `tea.Cmd` and tags it with a generation counter `m.gen`, so events from a replaced backend (engine switch, `/clear`, resume) are dropped. `handleEvent` turns events into transcript `block`s, and `render.go` renders them with per-block caching (`invalidate()`).
- `commands.go`: `localCmds` are slash commands handled by the app. Anything not listed there goes to the CLI as-is. Pickers, the palette, and the `@` file popup share `popup.go`.
- `saver.go`: the token savers. The concise-answer prompt is appended as a system prompt, and `leanDisallowed` tools are passed to the CLI (Claude engine only). `-full` or settings turn them off.
- `styles.go`: themes. `view.go`: layout, sidebar, status bar. `selection.go`: mouse drag-select/copy. `login.go`: `/login` flows including OpenRouter OAuth.
- Settings persist in `~/.config/teveus/` (`configDir()` in `extras.go`); `TEVEUS_CONFIG` overrides it, and UI tests set it to `t.TempDir()` so they don't touch real settings.

**Testing patterns.** Agent tests use an `httptest` server that returns scripted SSE replies (`script` in `internal/agent/engine_test.go`). UI tests build a `Model` directly and feed it `tea.Msg`s (see `api_test.go`, `selection_test.go`).
