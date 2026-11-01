# Changelog

What changed in each version of teveus. The newest version is at the top.
The format follows [Keep a Changelog](https://keepachangelog.com), and versions follow [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.32.0] - 2026-11-01

### Added

- `/scrub` masks the secrets in conversations saved before this version. New conversations, prompt history and exports are masked as they are written, so an API key you pasted, or an `.env` a command printed, comes back as `[redacted:…abcd]`, with the last four characters left so you can tell which key it was.

### Changed

- The phone page listens on `127.0.0.1` unless you name another address, so reaching it from a phone goes through a tunnel you set up rather than your whole network. Binding wider still works (`-phone 0.0.0.0:8787`) and now says so, warning that the page is plain HTTP.
- Opening the phone link now trades its token for an `HttpOnly`, `SameSite=Strict` cookie and redirects to `/`. The token stops appearing in the address bar, the browser history and the requests the page makes, and other windows' pages no longer carry their links: tapping one asks this window's server to open it.

### Fixed

- Updating no longer leaves the app frozen on the installer's last line. teveus wraps the terminal's input so a mouse report that arrives in pieces isn't typed as text, and a program started with the terminal handed over — the updater, and Claude Code's sign-in from `/login` — got that wrapper instead of the terminal itself. The command ran and finished, but the signal saying so never came back, so the window sat there until it was killed. Both now get the terminal directly, and a finished update still ends the app so the new version starts clean.

### Security

- The phone page can make permissions stricter but not looser. Switching to autopilot, and granting an "always allow" rule, now have to be done in the terminal, so a link that leaks can't hand the agent more than the window already had.
- Phone replies now carry `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `nosniff`, a nonce-based Content-Security-Policy and no-store caching, and the server bounds header size and read, write and idle timeouts.
- Commands, background jobs, hooks and MCP servers no longer inherit your API keys and tokens: `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_*` and similar are dropped from their environment. An MCP server still gets the variables its own configuration lists.
- A shell command that names a credential store (`~/.ssh`, `~/.aws`, `~/.git-credentials`, teveus's own config directory, cloud metadata) now asks first even in autopilot, instead of running quietly.
- WebFetch no longer reaches this machine or its network. Loopback, private, link-local and CGNAT addresses and cloud metadata are refused on the first request and on every redirect, and the address actually connected to is checked, so a DNS answer can't change in between. A new `WebFetchLocal` setting turns this off for a local dev server.
- `/undo` and `/rewind` now give a file back its original permissions instead of a fixed `0644`, so a restored `0600` key file doesn't become world-readable.

## [0.31.0] - 2026-11-01

### Added

- Every response is now written to a usage ledger, `~/.config/teveus/usage.jsonl`: the model, the project, the four token counts, and the provider's own cost when it reports one. Cost and tokens used to live only in memory, so nothing outside a running teveus could report them; a menu bar monitor, a script or a spreadsheet can now read them. One JSON line per billed response, appended, so it can be followed while teveus runs. `TEVEUS_CONFIG` moves the ledger with the rest of the config directory.
- Skills and slash commands now live in teveus's own folders: `.teveus/skills` and `.teveus/commands` in the project, or `skills` and `commands` in the config directory to use them in every project. Skills follow the [Agent Skills](https://agentskills.io) format, so one written for another agent works unchanged, and `.agents` and `.claude` are still read (teveus's own folders win when a name is defined twice).
- A skill's folder comes back with its instructions, so skills that bundle scripts or reference files next to `SKILL.md` work.
- Skills can say `disable-model-invocation: true` to stay out of the model's hands and only run when you type the command, and `user-invocable: false` to hide from the slash menu. Both are honoured wherever the skill sits.

### Fixed

- Reading your own skills no longer counts as reading the config directory. The config directory is off limits because it holds your API keys and rules, which also hid the skills in it; that exemption covers reading only, so a reply can't write a skill that you would later load.
- Models behind OpenCode and other gateways now report their real context window, so auto-compaction fires before a session outgrows it. Those gateways nest the limit as `limit.context` and the prices as `cost.text`, where OpenRouter uses a flat `context_length` and string prices; only the second shape was read, so the window came back as zero and compaction — which is skipped when the window is unknown — never ran. Prices were missing from the picker for the same reason.
- `max` reasoning effort now reaches models that take it. OpenAI-style APIs were capped at `high` three times over: in the protocol ceiling, in the request builder, and in the effect it had on the effort picker. A gateway that publishes each model's levels (`capabilities.reasoning.effort`, as OpenCode does) is now believed over that guess, so DeepSeek, which takes `low`/`high`/`max`, is no longer quietly held at `high`.

## [0.30.0] - 2026-11-01

### Fixed

- Long unbroken text no longer spills out of the chat column. Thai and Chinese are usually written without spaces, and a URL has none either, so a reply could draw straight over the sidebar. Such lines now wrap inside their own column, and the sidebar keeps its place on every row.
- A wrapped line no longer swallows the rest of its message. The wrapper reused the split slice's array for its output, and breaking one line appends more entries than it consumes, so the lines after it were overwritten and vanished — a long command in a `Bash` result could cut the reply short.
- Being stopped by the terminal no longer wrecks the screen. `ctrl+z` was handled, but the kernel can also stop the process on its own — `kill -TSTP`, a closing window, or a background process group reading the terminal (`SIGTTIN`, which the shell reports as `suspended (tty input)`). Those went unhandled, so the process stopped with the alternate screen, the key protocol and mouse capture still on, and the shell echoed every mouse report as text. They now restore the terminal before stopping and take it back on `fg`.
- The mouse keeps working after `fg`. Releasing the terminal turns mouse capture off, and restoring it doesn't turn it back on, so the wheel stopped scrolling after the first suspend until the app was restarted.
- Thai and Lao text no longer pushes the right-hand border inwards. The width libraries count SARA AM (`ำ`) as one cell while terminals draw two, so a line holding it measured a bit narrower than it drew, wrapped, and shifted everything below it — the sidebar edge wandered a row at a time. Tool output, the todo list, the sidebar's own sections, the status bar, the input box and the popups each build their text in a different place, and now each one measures it the way the terminal does.
- The frame is now forced to the window's size before it is drawn. A row that wraps, or a bottom area a line taller than measured, used to leave the previous frame showing through — a missing input border with the old status bar still on screen. Any such mistake is now clipped and padded instead of breaking the display.

### Changed

- Built with Go 1.27, whose runtime and standard library bring the fixes and speedups of that release. Releases are still built for the same platforms.
- Long sessions stay smooth. While a reply streams, teveus now redraws only the message that changed instead of rebuilding the whole transcript: a 6000-block session went from about 22 ms to 2.4 ms per chunk. The welcome text no longer lingers above the first message.

### Security

- Updated the libraries behind web fetching and images. A page that WebFetch downloads could previously make the HTML parser do quadratic work and stall the app; `govulncheck` now reports no vulnerabilities.

## [0.29.0] - 2026-10-26

### Changed

- The test suite covers much more of the app: the Claude Code client is now exercised against a stand-in for the CLI, the store and search providers are verified, and every slash command, palette entry and event type is run at least once. Coverage went from 44% to 91% in the CLI package, 72% to 82% in the UI, and 77% to 78% in the engine.

## [0.28.0] - 2026-10-25

### Fixed

- `ctrl+z` suspends cleanly and `fg` brings teveus back. Before, the terminal stopped the process while the app was still in its alternate screen, so the shell came back printing into a screen that was never restored — stray `64;29;18M` text, and the window had to be closed. A signal that doesn't come from `ctrl+z` now restores the terminal before it stops the process.

## [0.27.0] - 2026-10-25

### Changed

- Replies on the phone page are formatted instead of shown as raw Markdown: headings, bold and italics, inline code, code blocks, lists, quotes and links, sized for a phone screen. The renderer builds the page's elements directly and only links to http(s), so a reply can't put its own HTML or a script link on the page.

## [0.26.0] - 2026-10-25

### Fixed

- Effort now means the same thing everywhere. The level you pick is lowered to what the model actually takes — `max` on an OpenAI-style API sends `high` instead of a word it rejects — and the sidebar shows the level in use, with `(max capped)` when your pick had to come down.
- A model that refuses the effort setting is remembered, so the next request doesn't spend a retry on the same refusal. The refusal is paid once ever, and announced once in the conversation instead of failing silently.

## [0.25.0] - 2026-10-25

### Changed

- Minimal code now states four rules instead of examples: take the answer's shape from the request, add what was asked for and nothing else; when a request reads two ways, the simpler value wins; a public name is a promise; a standard-library call is the implementation. Strict ends with a line-by-line pass over the diff and a check that the leaner result still does the whole job.
- A reply with neither words nor a tool call is asked again instead of ending the turn silently, which looked like the agent had given up.
- On an OpenCode Go plan, the cost you see is the list price of the tokens instead of $0, so the sidebar and the turn footer show what the work would have cost.

## [0.23.0] - 2026-10-25

### Added

- `/commit` finishes the job: it checks there is something to commit, then the AI stages what this work touched, writes the message in your project's style and makes the commit — no trip to another terminal. Add words (`/commit only the parser`) to steer what goes in. It never commits secrets, never uses `--no-verify`, and never pushes.

## [0.22.0] - 2026-10-25

### Added

- Type while the agent is working and it now reads you at its next step instead of waiting for the whole turn to finish: the message joins the running conversation between tool calls, and appears in the transcript where it landed. On the API engine the box says "Say it now"; Claude Code has no way to take a message mid-turn, so there it still queues for the end of the turn.

## [0.21.0] - 2026-10-25

### Added

- Messages sent from the phone while a turn is still running now wait their turn in view: the page lists them under the composer ("next: …", "then: …") and sends them in order as soon as the current work finishes, instead of appearing to vanish.

## [0.20.0] - 2026-10-25

### Added

- The phone page shows the sub-windows the terminal opens — `/model`'s list, `/effort`'s slider, an API-key field, the "do you trust this folder?" question — and you can answer them from the phone. Before, those commands ran but their window only appeared in the terminal.

## [0.19.1] - 2026-10-25

### Changed

- The README now covers the phone page: what it does, how to keep it off a network you don't trust, and how to revoke its link.

## [0.19.0] - 2026-10-25

### Fixed

- After an update, teveus now quits to a clean shell and tells you to start it again, instead of sitting in a terminal that ignored ctrl+c and had to be closed with the window's button. The phone page shuts down too, so its port is released.

## [0.18.0] - 2026-10-25

### Fixed

- The phone page no longer zooms: pinch, double-tap and iOS's zoom-on-focus are all held off, so the layout stays put while you type.

## [0.17.0] - 2026-10-25

### Added

- The phone page runs your slash commands: type `/` or tap the `/` key to get a searchable list of everything the app takes, and picking one runs it as if you had typed it.

### Changed

- The phone page is quieter and denser: one status line, replies as plain text rather than cards, and tool calls as slim indented lines, so more of the conversation fits on a phone screen.

## [0.16.0] - 2026-10-25

### Added

- The phone page lists your other teveus windows too, not just the sessions in one: run teveus in several terminal tabs and all of them show up with what each is doing. Tapping one opens that window's page.
- Every window shares one link, so saving it once on your phone reaches all of them.

### Fixed

- A window that was idle served nothing to the phone page; it now keeps its state current.

## [0.15.0] - 2026-10-25

### Added

- The phone page can switch between your open sessions, showing which are idle, working or waiting for approval.
- The phone link no longer changes between runs, so you can save it on your phone's home screen once. Delete `~/.config/teveus/phone-token` to give out a new one.

## [0.14.0] - 2026-10-25

### Added

- `/phone` turns the phone page on or off and prints its link again.
- Switch permission mode from the phone page's header, and see why a tool asked, the command or file it would touch, and what denying does.

### Fixed

- The phone page's link now opens: it names an address the page really listens on, and the page moves to the next free port when 8787 is taken instead of failing quietly.
- The phone page reads like a chat, with your messages on the right, replies as cards and tools as quiet lines, and it opens on the newest message.

## [0.13.0] - 2026-10-25

### Added

- Prompt and approve from a phone. `/settings` → Phone page turns on a small local web page whose link you open on your phone: you can type a message, watch the replies, and allow or deny tools. It's off by default; anyone with the link can prompt and approve while it's on.

## [0.12.0] - 2026-10-25

### Added

- With OpenCode Go, the sidebar shows how much of your rolling, weekly and monthly allowances you've used, with reset times.

## [0.11.0] - 2026-10-25

### Added

- Connect OpenCode Go or OpenCode Console with `/login`. Go requests carry the session ID OpenCode asks for, so long conversations keep their routing and prompt cache.

## [0.10.0] - 2026-10-25

### Added

- Windows: install with PowerShell, and let teveus update itself with the same verified installer.
- The welcome screen suggests actions that fit the project it finds, such as running its tests or explaining its code.

### Changed

- The welcome screen uses the teveus block wordmark again instead of spaced-out letters.

## [0.9.0] - 2026-10-25

### Changed

- A cleaner, easier-to-read look: a teal-and-violet dark theme, brighter text, calmer section labels, card-style messages, simpler tool output, and a redesigned welcome screen, input box and approval prompt.

### Fixed

- On Windows, interrupting a shell command (or letting it time out) now stops the programs it started, instead of leaving them running.

## [0.8.0] - 2026-10-25

### Added

- `/rewind` shows the files and a diff of what it will change before anything is touched: press enter to restore, or esc to go back.

## [0.7.0] - 2026-10-25

### Added

- `/rewind` restores the conversation and the files it changed to before any earlier turn, not just the last one (Direct API engine).

### Security

- Updated the Go build to 1.26.10, fixing a standard-library certificate-check vulnerability.

## [0.6.7] - 2026-10-13

### Changed

- Uses less CPU while idle: the screen refresh slows down when nothing is running.

## [0.6.6] - 2026-10-09

### Fixed

- The waiting-message queue uses a plain marker instead of emoji.

## [0.6.5] - 2026-10-09

### Added

- Messages you type while a reply is still running now wait in a faint queue above the input and send one after another, instead of interrupting the turn.
- Click a waiting message (or press ctrl+e) to pull it back into the input and edit it.

### Fixed

- The side panel no longer breaks the saver list mid-line, and shows the current model.

## [0.6.4] - 2026-10-09

### Fixed
- Copying code or text that wrapped across several lines on screen now pastes it as one line, the way it was written, and code indented with tabs keeps its tabs.

## [0.6.3] - 2026-10-04

### Fixed
- Scrolling fast with the mouse or trackpad could still type text like `[<65;28;24M` into the message box while the screen was busy redrawing.

## [0.6.2] - 2026-10-04

### Fixed
- Scrolling with the mouse or trackpad could type text like `[<65;28;24M` into the message box, in terminals that send mouse events in pieces.
- `cmd+v` with only an image on the clipboard now attaches the image instead of doing nothing.

## [0.6.1] - 2026-09-24

### Fixed
- Scrolling with the mouse wheel could fill the message box with your earlier prompts instead of scrolling the conversation, in terminals that turn the wheel into arrow keys. The wheel no longer sends arrow keys while teveus is open, so ↑ and ↓ only browse your earlier prompts.

### Added
- `shift+↑` and `shift+↓` scroll the conversation, next to the mouse wheel and `pgup`/`pgdn`.

## [0.6.0] - 2026-09-24

### Added
- teveus asks "Do you trust this folder?" the first time you open it in a folder, before anything is read or started. Answer yes once and it doesn't ask again there or in the folders inside it; answer no and it exits.
- Links in the conversation are clickable: cmd+click (ctrl+click on Windows and Linux) opens them in terminals that support links, including ones too long to fit on one line.

### Fixed
- Scrolling down could suddenly scroll the conversation up instead, after a mouse release the terminal never reported (for example when you cmd+clicked a link). A drag that lost its release now ends.

## [0.5.0] - 2026-09-24

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

[Unreleased]: https://github.com/stawan15/teveus/compare/v0.32.0...HEAD
[0.32.0]: https://github.com/stawan15/teveus/compare/v0.31.0...v0.32.0
[0.31.0]: https://github.com/stawan15/teveus/compare/v0.30.0...v0.31.0
[0.30.0]: https://github.com/stawan15/teveus/compare/v0.29.0...v0.30.0
[0.29.0]: https://github.com/stawan15/teveus/compare/v0.28.0...v0.29.0
[0.28.0]: https://github.com/stawan15/teveus/compare/v0.27.0...v0.28.0
[0.27.0]: https://github.com/stawan15/teveus/compare/v0.26.0...v0.27.0
[0.26.0]: https://github.com/stawan15/teveus/compare/v0.25.0...v0.26.0
[0.25.0]: https://github.com/stawan15/teveus/compare/v0.24.0...v0.25.0
[0.24.0]: https://github.com/stawan15/teveus/compare/v0.23.0...v0.24.0
[0.23.0]: https://github.com/stawan15/teveus/compare/v0.22.0...v0.23.0
[0.22.0]: https://github.com/stawan15/teveus/compare/v0.21.0...v0.22.0
[0.21.0]: https://github.com/stawan15/teveus/compare/v0.20.0...v0.21.0
[0.20.0]: https://github.com/stawan15/teveus/compare/v0.19.1...v0.20.0
[0.19.1]: https://github.com/stawan15/teveus/compare/v0.19.0...v0.19.1
[0.19.0]: https://github.com/stawan15/teveus/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/stawan15/teveus/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/stawan15/teveus/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/stawan15/teveus/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/stawan15/teveus/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/stawan15/teveus/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/stawan15/teveus/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/stawan15/teveus/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/stawan15/teveus/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/stawan15/teveus/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/stawan15/teveus/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/stawan15/teveus/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/stawan15/teveus/compare/v0.6.7...v0.7.0
[0.6.7]: https://github.com/stawan15/teveus/compare/v0.6.6...v0.6.7
[0.6.6]: https://github.com/stawan15/teveus/compare/v0.6.5...v0.6.6
[0.6.5]: https://github.com/stawan15/teveus/compare/v0.6.4...v0.6.5
[0.6.4]: https://github.com/stawan15/teveus/compare/v0.6.3...v0.6.4
[0.6.3]: https://github.com/stawan15/teveus/compare/v0.6.2...v0.6.3
[0.6.2]: https://github.com/stawan15/teveus/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/stawan15/teveus/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/stawan15/teveus/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/stawan15/teveus/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/stawan15/teveus/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/stawan15/teveus/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/stawan15/teveus/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/stawan15/teveus/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/stawan15/teveus/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/stawan15/teveus/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/stawan15/teveus/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/stawan15/teveus/releases/tag/v0.1.0
