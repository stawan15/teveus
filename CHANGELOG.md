# Changelog

What changed in each version of teveus. The newest version is at the top.
The format follows [Keep a Changelog](https://keepachangelog.com), and versions follow [Semantic Versioning](https://semver.org).

## [Unreleased]

### Added
- Your own slash commands and skills on the Direct API engine. Markdown files in `.claude/commands/` (for example `review.md` becomes `/review`, with `$ARGUMENTS` and `$1` filling in what you type after it) and in `.claude/skills/`, in the project or in `~/.claude`, show up in the `/` menu. The model can also load a skill on its own when a task matches it.
- `/hooks`: run your own commands before or after the model's tool calls, such as a formatter after each edit or a check that blocks a command. They are kept in teveus's settings folder, never in the project, so a repository can't run commands by itself.
- Long-running commands in the background on the Direct API engine: the model can start a dev server or a watcher, read its output later and stop it. They all stop when you close the session.
- Notebooks (`.ipynb`) on the Direct API engine: the model reads them cell by cell, without the bulky outputs, and can replace, insert or delete a cell.
- `/search text` finds words in the open conversation (pick a match to jump to it) and in your earlier conversations in this folder (pick one to resume it).
- `/worktree`, or "New session in a git worktree" in `/sessions`: a session gets its own copy of the repository on its own branch, so several sessions can edit at once without overwriting each other. Closing it removes the copy unless it holds uncommitted work.
- `/subagent` (or `/settings` → Research subagent model): run the research subagents on a cheaper model. Off until you choose one; they use the conversation's model by default, and fall back to it if the chosen one can't be reached.
- What each session has used shows in `/sessions`, with the total, and in the status bar when the sidebar is hidden.

### Changed
- Long sessions on the Direct API engine use fewer tokens without losing anything: big tool output that is many steps old (a file read, a test log) is replaced in what the model is sent by a one-line note, and the model runs the tool again if it needs it. It happens in batches so the provider's prompt cache keeps working, and your transcript and `/resume` still show the full output.
- Reading a file that hasn't changed since the model last read it now answers "unchanged, it's above" instead of sending the file again.
- When a conversation nears the model's context limit, old tool output is dropped first, and the conversation is only summarised if that isn't enough. That saves the cost of the summary.
- A session you aren't looking at now sends a desktop notification whenever it finishes or needs approval, naming the session, however short its turn was.

## [0.4.0] - 2026-09-24

### Added
- Several conversations at once in one window. `/sessions` lists them and lets you start, switch to or close one; `alt+1` to `alt+9` jump straight to a session. Sessions keep working while you look at another, and a line of tabs under the header names each one, with `●` for one that is working and `!` for one waiting for your approval. On macOS Terminal, turn on "Use Option as Meta Key" for the `alt` keys.

### Security
- Built with Go 1.26.8, which fixes six vulnerabilities in Go's standard library (net/http, crypto/tls, net/url, encoding/xml and encoding/asn1). CI now checks for known vulnerabilities on every push.

### Changed
- Typing `/` now lists only the eleven commands most people need (`/model`, `/login`, `/sessions`, `/mode`, `/resume`, `/undo`, `/diff`, `/clear`, `/settings`, `/help`, `/exit`). Every other command, including Claude Code's own, still works and appears as soon as you type part of its name.
- Commits and pull requests made through teveus no longer carry a "Co-Authored-By" or "Generated with" line: not from Claude Code, and not from the Direct API engine. To bring them back, turn on "Credit AI in commits" in `/settings`.

## [0.3.2] - 2026-09-23

### Added
- teveus tells you when a new version is out and asks before updating, showing what's new. It checks GitHub at most once a day; choose "Later", skip that version, or turn checks off in `/settings`. `/update` checks right away.
- Updating from inside teveus uses the way you installed it (the installer, with its checksum check, or `go install`).

### Changed
- The Claude Code notice and the update prompt are easier to scan, with a colour for each point and each choice.
- The installer's closing message now says to connect Claude Code with `/login`.

## [0.3.1] - 2026-09-23

### Changed
- teveus now starts with nothing connected. Choose where the AI comes from with `/login`: your installed Claude Code, or any provider with your own key.
- Before Claude Code is connected for the first time, teveus explains that it runs your own Claude Code under Anthropic's terms and asks you to confirm. The notice appears only when you choose Claude Code, and only once.

### Fixed
- Popups opened in the background (like the Claude Code notice) no longer push the bottom of the screen out of view.

## [0.3.0] - 2026-09-23

### Added
- `/effort` sets how hard the model thinks, from `low` to `max`, on both engines. Models that don't support it simply run at their default.
- Settings with levels (effort, minimal code, guidance) use a coloured slider: move with ← →, and the whole box takes the colour of the level you pick.
- `/minimal` asks the model to write only the code the task needs (off, lite, full or strict), and `/trim` reviews your changes for code you don't need.
- `/purge` deletes saved conversations and prompt history in one step.
- A privacy page ([PRIVACY.md](https://github.com/stawan15/teveus/blob/main/PRIVACY.md)) listing exactly what is sent where and what is stored, and a security page ([SECURITY.md](https://github.com/stawan15/teveus/blob/main/SECURITY.md)) with a private way to report problems.

### Changed
- The usage savers (short answers, lean tools, minimal code) are now off until you turn them on.
- Saved conversations are deleted after 30 days. Change this with `keepSessionsDays` in settings, or set it to `-1` to keep them.

### Fixed
- When a provider is busy or rate-limited, teveus waits and tries again (up to three times) instead of stopping.
- A reply cut off by the model's output limit no longer runs a half-written tool call.
- Claude models used through the Direct API now keep their reasoning between tool calls, as the API requires.

### Security
- teveus's file tools can no longer read or change credential files (API keys, SSH keys, cloud logins, Claude Code's login) in any mode.
- Reading or searching outside your project asks first, and auto-edit mode only edits files inside the project.
- Plan mode asks before using the web.
- Prompt history and settings are saved readable only by you.

## [0.2.0] - 2026-09-23

### Added
- Paste or drop images: drop a screenshot on the terminal or press `ctrl+v`, and it's attached as `[Image #1]`. Large images are scaled down automatically.
- `shift+enter` starts a new line (or `\` then `enter` in terminals that can't tell the keys apart).
- Long pastes fold into `[Pasted text #1 +40 lines]` so the input stays tidy.
- For the Direct API engine:
  - reading web pages, and web search with a Brave or Tavily key
  - sub-agents that research in parallel
  - MCP servers from `.mcp.json`, started only after you approve them with `/mcp`
  - "always allow" rules saved per project, managed with `/permissions`
  - long conversations compacted automatically before they outgrow the model
- `teveus -p "…"` runs one prompt without the interface, for scripts (`-json` adds cost and token counts).
- Faint lines between turns make long conversations easier to follow.

### Changed
- The README explains that teveus is independent of Anthropic and runs your own Claude Code.

### Fixed
- Long conversations scroll and stream much faster.
- Wide approval prompts no longer overflow small windows.

## [0.1.2] - 2026-09-23

### Fixed
- Thai text containing "ำ" no longer breaks the layout (lines shifted, the header disappeared, parts of the screen were drawn twice).

## [0.1.1] - 2026-09-23

### Fixed
- `teveus -version` shows the right version when installed with `go install`.

## [0.1.0] - 2026-09-23

### Added
- First release: a friendly terminal for AI coding with two engines behind one interface.
  - Claude Code: runs your installed Claude Code.
  - Direct API: your own keys for OpenAI, Anthropic, Google Gemini, OpenRouter (with browser login), Groq, DeepSeek, xAI, Mistral, any OpenAI-compatible service, or local models with Ollama and LM Studio.
- Every change is shown as a coloured diff and waits for your approval. Four plain-language modes: ask first, auto-edit, plan only, autopilot.
- Command palette (`ctrl+k`), `@` to mention files, prompt history, `/undo`, `/resume`, `/diff` and `/export`.
- Sidebar with cost, context size, plan progress and usage limits.
- Six themes including high contrast, three guidance levels, reduce motion, and full keyboard use.
- A three-step first-run setup.

[Unreleased]: https://github.com/stawan15/teveus/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/stawan15/teveus/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/stawan15/teveus/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/stawan15/teveus/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/stawan15/teveus/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/stawan15/teveus/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/stawan15/teveus/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/stawan15/teveus/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/stawan15/teveus/releases/tag/v0.1.0
