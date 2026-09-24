# Changelog

What changed in each version of teveus. The newest version is at the top.
The format follows [Keep a Changelog](https://keepachangelog.com), and versions follow [Semantic Versioning](https://semver.org).

## [Unreleased]

### Security
- Built with Go 1.26.8, which fixes six vulnerabilities in Go's standard library (net/http, crypto/tls, net/url, encoding/xml and encoding/asn1). CI now checks for known vulnerabilities on every push.

### Changed
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

[Unreleased]: https://github.com/stawan15/teveus/compare/v0.3.2...HEAD
[0.3.2]: https://github.com/stawan15/teveus/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/stawan15/teveus/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/stawan15/teveus/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/stawan15/teveus/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/stawan15/teveus/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/stawan15/teveus/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/stawan15/teveus/releases/tag/v0.1.0
