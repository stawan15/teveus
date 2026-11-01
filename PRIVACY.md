# Privacy

teveus is a program that runs on your computer. It has no servers, no accounts, and no telemetry: it doesn't collect analytics, crash reports or usage data. The author receives nothing from your use of it. The one thing it does on its own is ask GitHub, at most once a day, whether a newer version exists (see below; you can turn it off).

What does leave your computer goes to services **you** choose, under **their** terms.

## What is sent, and to whom

| When | What is sent | To |
|---|---|---|
| You use the **Claude Code** engine | Your messages, and whatever Claude Code reads or runs for them | Anthropic, by your installed Claude Code, under [Anthropic's terms and privacy policy](https://www.anthropic.com/legal/privacy). teveus only starts the `claude` program; it never sees or stores your Claude login. |
| You use the **Direct API** engine | Your messages, images you attach, the contents of files and command output the model reads while working, and (for OpenCode) a stable session ID | The AI provider whose model you picked (OpenAI, Anthropic, Google, OpenRouter, OpenCode, …), under that provider's terms. Local models (Ollama, LM Studio) stay on your machine. If you pick a separate model for research subagents (`/subagent`), what those subagents read goes to that model's provider instead. |
| teveus starts on the Direct API engine | A request for the list of models | Each provider you've connected |
| You use the **OpenCode Go** plan | A request for your plan's usage, to draw the sidebar bars (no conversation content) | opencode.ai |
| The model uses **WebFetch** (you approve it) | A request for that web address | That website |
| The model uses **WebSearch** (you approve it) | The search words | Brave Search or Tavily, whichever key you connected |
| The model uses an **MCP** tool | Whatever that tool is given | The MCP server you configured (a program on your computer, or the address you set) |
| You log in to **OpenRouter** in the browser | A one-time login exchange | openrouter.ai |
| teveus starts (at most once a day) | A request for the latest version number, with the app name and version as the user agent | GitHub (api.github.com). Turn it off in `/settings` → Check for updates, or with `"noUpdateCheck": true`. Builds from source never check. |
| You choose **Update now**, or run an installer (`install.sh` / `install.ps1`) | A download request | GitHub |

Nothing else is sent. Your clipboard is only read when you press `ctrl+v`, and only written when you copy.

The phone page (`/settings` → Phone page, off by default) is a web server on your own computer: your phone talks to it over your network, and nothing goes anywhere else. By default it listens on `127.0.0.1` only, so reaching it from a phone means a tunnel you set up (Tailscale, `ssh -L`) rather than your whole network. Which reasoning effort each model accepted is kept in `~/.config/teveus/effort.json` (readable only by you) so a refusal is paid once rather than every request. The link's token is kept in `~/.config/teveus/phone-token` (readable only by you) so the link stays the same between runs; delete that file to give out a new one. Opening the link stores that token in your browser as an `HttpOnly` cookie and redirects to `/`, so the address bar, your history and the page's scripts stop carrying it. The page holds no keys and no stored history, and it stops when you turn it off or quit teveus.
Each window that serves a page writes a small handle to `~/.config/teveus/phone/` (its port, title, project and token) so another window's page can list it; the file is removed when the window stops or the page is turned off. A handle with no running process behind it is deleted on sight. Other windows' pages show the handle's title and state, never its token: opening one asks this window's server for the link.

## What is stored on your computer

Everything lives in `~/.config/teveus/` (on Windows, `%AppData%\teveus\`; `TEVEUS_CONFIG` moves it). Files are readable only by your user account.

| File | Contents | Kept |
|---|---|---|
| `auth.json` | API keys you saved with `/login` | Until you `/logout` |
| `settings.json` | Your preferences, and when the last update check ran | Always |
| `history.json` | Your last 500 prompts, for the ↑ key (keys you pasted are masked) | Until `/purge` |
| `sessions/` | Direct API conversations (text and images), for `/resume`; API keys and passwords are masked before they are written | 30 days, then deleted (`"keepSessionsDays"` in settings; `-1` keeps them) |
| `permissions.json` | Allow/deny rules you saved | Until you remove them (`/permissions`) |
| `hooks.json` | Commands you set to run around tool calls (`/hooks`) | Always |
| `trusted-folders.json` | Folders you said you trust | Always |
| `mcp-approved.json` | Which projects' MCP servers you approved | Always |

With a git worktree session, teveus also creates `.teveus/worktrees/` inside your repository and lists `.teveus/` in the repository's `.git/info/exclude`, so it stays out of `git status`. A clean worktree is removed when its session closes.

`/purge` deletes the saved conversations and prompt history at once. Deleting the folder removes everything, including keys. Claude Code keeps its own history in `~/.claude/`; teveus doesn't manage it.

teveus's own tools never read `auth.json` or other credential files (SSH keys, cloud credentials, Claude Code's login), even if a model asks them to.

Commands, background jobs, hooks and MCP servers start without the credential variables from your shell (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_*` and the like). An MCP server still gets exactly the variables its own configuration gives it. This doesn't apply to anything you run yourself outside teveus, and it isn't a sandbox: a command you approve runs with your account's permissions.

Conversations and prompt history are passed through a redactor before they are written, and `/scrub` cleans files saved by older versions. It catches the key shapes it knows and the exact keys teveus holds; a secret it doesn't recognise can still be stored, so review an export before you share it.

## Changes

If a future version sends anything new, this page will say so before that version ships.
