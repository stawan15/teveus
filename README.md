<div align="center">

# teveus

**teveus is a free, open-source AI coding assistant that runs in your terminal.**
Run Claude Code, or any AI provider with your own key, in one fast, good-looking app.

[Website](https://www.teveus.xyz) · [Docs](https://www.teveus.xyz/docs) · [Releases](https://www.teveus.xyz/releases)

![teveus fixing a bug: Claude reads the code, proposes a change, you approve it](docs/demo.gif)

</div>

## Install

**macOS and Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/stawan15/teveus/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/stawan15/teveus/main/install.ps1 | iex
```

Works on macOS, Linux and 64-bit Windows. Run the same installer again to update.

> **Want Claude models on your Claude plan?** Install [Claude Code](https://claude.com/claude-code) from Anthropic and sign in to it.
> teveus runs your installed Claude Code as it is. You sign in through Anthropic's own login, and teveus never sees your Claude credentials.
> For OpenAI, Gemini, OpenRouter and others you don't need anything else.

## Get started

1. Open a terminal in your project folder.
2. Run `teveus`, then connect an AI with `/login`: your installed Claude Code, or any provider with your own key. Nothing is connected until you choose.
3. Type what you want in plain words, like *"find the bug in the login page and fix it"*, and press **enter**.

teveus shows you every change **before** it happens. Press **y** to allow it or **n** to say no.

The first time, a short setup (3 questions) helps you pick how much guidance you want, a colour theme, and how to connect.

## What it looks like

| Approve changes before they happen | Every action one search away |
|:---:|:---:|
| ![Approval prompt showing the exact diff](docs/approval.png) | ![Command palette](docs/palette.png) |
| **Welcome screen with starter ideas** | **Themes preview live** |
| ![Welcome screen](docs/welcome.png) | ![Theme picker](docs/theme.png) |

## What you can do

| | |
|---|---|
| 💬 **Ask in plain words** | Explain code, fix bugs, write tests, review changes |
| ✅ **Stay in control** | See each change as a coloured diff and approve it first |
| 🔌 **Use any AI** | Claude Code (with your own Claude account), OpenAI, the Anthropic API, Gemini, OpenRouter (hundreds of models, many free), Groq, DeepSeek, xAI, Mistral, OpenCode (Go or Console), or local models (Ollama, LM Studio) |
| 🖼️ **Show, don't describe** | Drop a screenshot on the terminal or press `ctrl+v`; long pastes fold into `[Pasted text #1 +40 lines]` |
| 🌐 **Web, MCP and helpers** | On the Direct API engine: reads web pages, searches the web (Brave or Tavily key), uses MCP servers from `.mcp.json`, and sends research to parallel sub-agents. Claude Code brings its own. |
| 🔒 **Rules you set once** | On the Direct API engine: "always allow `go test`" is saved per project, and `/permissions` lists, adds and removes allow/deny rules |
| 🧩 **Make it yours** | On the Direct API engine: your slash commands and skills from `.claude/commands` and `.claude/skills`, `/hooks` to run your own commands around tool calls, background commands for dev servers, and notebook editing |
| 🌿 **Sessions that don't collide** | `/worktree` gives a session its own git worktree; `/sessions` shows what each one has used |
| 📱 **Take it to your phone** | `/phone` serves a page your phone opens: prompt, watch replies and approve tools from the sofa. Every open window shows up there, and the link stays the same between runs |
| 🔎 **Find it again** | `/search text` looks through this conversation and earlier ones |
| ↩️ **Undo and rewind** | `/undo` puts files back the way they were; `/rewind` shows what will change, then goes back to any earlier turn |
| 🕘 **Pick up later** | `/resume` reopens earlier conversations |
| 💸 **Spend less, when you want** | Off until you turn them on: `/concise` for short answers, `/lean` to send fewer tool definitions, and `/minimal` (a slider from off to strict) so the model reuses what exists and writes only what the task needs |
| ✂️ **Trim the fat** | `/trim` reviews your uncommitted changes for code you don't need; `/trim all` checks the whole project |
| 🪙 **Long sessions stay cheap** | On the Direct API engine, old tool output is swapped for a one-line note (the model can run it again), unchanged files aren't re-sent, and `/subagent` runs research on a cheaper model |
| 🎨 **Make it yours** | 6 themes (including high contrast), 3 guidance levels, reduce motion |

## Keys to know

You only need the first three. Press **`?`** inside teveus to see the rest.

| Key | Does |
|---|---|
| `enter` | Send your message |
| `shift+enter` | New line (or `ctrl+j`, or `\` then `enter`) |
| `ctrl+k` | Find any action (command palette) |
| `?` | Show all shortcuts |
| `shift+tab` | Switch mode: **ask first** → **auto-edit** → **plan only** → **autopilot** |
| `@` | Attach a file by name |
| `ctrl+v` | Paste an image from the clipboard |
| `esc` `esc` | Stop the current task |
| drag with the mouse | Select text (it's copied when you let go) |
| `ctrl+c` `ctrl+c` | Quit |

## Commands

Type `/` to see the main ones; keep typing to find the rest. The most useful:

| Command | Does |
|---|---|
| `/login` | Sign in to Claude Code, or connect another AI provider |
| `/model` | Switch model (search by name, or type `free`) |
| `/effort` | How hard the model thinks: a slider from auto and low up to max |
| `/undo` | Undo the last change |
| `/rewind` | Go back to an earlier turn: review its files and diff first |
| `/sessions` | Run several conversations at once: switch, start or close one (`alt+1` to `alt+9` jump to one) |
| `/resume` | Reopen an earlier conversation |
| `/search` | Find text in this and earlier conversations |
| `/worktree` | Start a session in its own git worktree |
| `/hooks` | Commands that run before or after tool calls |
| `/diff` | See what changed |
| `/init` | Let the AI write notes about your project so it works better |
| `/settings` | All preferences in one place |
| `/mcp` | See MCP servers, and approve a project's `.mcp.json` |
| `/permissions` | Saved allow/deny rules for tools |
| `/phone` | Prompt and approve from your phone: turns the phone page on or off, or shows its link |
| `/update` | Check for a new version and install it |
| `!command` | Run a shell command, e.g. `!npm test` |

## From your phone

Leave a long task running and walk away: `/phone` turns on a small web page served by teveus itself, and you open its link on your phone.

- **Prompt, watch, approve.** Type a message, read the replies, and answer the same approval question the terminal asks, with Allow, Always or Deny. There's a stop button for a turn that's gone wrong.
- **Every window.** Each teveus you have open in a terminal shows up, with what it's doing, so the phone reaches all of them, not just one.
- **Same shortcuts.** Type `/` on the page to search and run any command the app has.
- **Off until you ask.** It starts from `/phone` or `/settings`, and stops when you turn it off or quit.
- **One link, kept.** It stays the same between runs so you can save it to your home screen. Delete `~/.config/teveus/phone-token` to give out a new one and revoke the old link.

Anyone with the link can prompt and approve while the page is on, so keep it on a network you trust: your home Wi-Fi, or a [Tailscale](https://tailscale.com) address if you want it from anywhere. Nothing leaves your computer except the questions you type.

## Connect other AI providers

Run `/login` inside teveus and pick one:

- **OpenRouter**: log in with your browser, no key to copy. Hundreds of models; type `free` in `/model` to find free ones.
- **OpenAI, Anthropic, Gemini, Groq, DeepSeek, xAI, Mistral, OpenCode**: paste an API key. It's checked before saving and stored only on your computer. For OpenCode, choose Go (subscription) or Console (pay-as-you-go).
- **Ollama or LM Studio**: runs on your own machine; nothing to set up if it's running.

## Use it from scripts

`teveus -p "your prompt"` runs one prompt without the UI and prints the reply; add `-json` for the cost and token counts.
Tools that need approval are refused unless you pass `-mode acceptEdits` or `-mode auto`.

## Questions

<details>
<summary><b>Do I use my Claude plan, or pay per token?</b></summary>

With the **Claude Code** engine, teveus runs your installed Claude Code, and Claude Code uses whatever you signed in to it with: your Claude plan or an API key.
teveus only starts the `claude` program and shows what it does. It never reads, stores or sends your Claude login, and the first time it asks you to confirm that you want it to run your Claude Code.
With **Direct API** (`/engine`), you pay the provider per token with your own key.
</details>

<details>
<summary><b>Is teveus made by Anthropic?</b></summary>

No. teveus is an independent open-source project. It isn't affiliated with, endorsed by, or supported by Anthropic.
</details>

<details>
<summary><b>How do I select and copy text?</b></summary>

Drag with the mouse; the text is copied when you let go. Double-click selects a word and triple-click a line.
`ctrl+y` copies the whole last reply. Prefer your terminal's own selection? Run `/mouse`.
</details>

<details>
<summary><b>Where are my settings and keys?</b></summary>

In `~/.config/teveus/`. Keys are in `auth.json`, readable only by you.
</details>

<details>
<summary><b>How do I uninstall?</b></summary>

**macOS and Linux**

```sh
rm ~/.local/bin/teveus
rm -rf ~/.config/teveus   # also removes settings and saved keys
```

**Windows (PowerShell)**

```powershell
Remove-Item -Recurse "$env:LOCALAPPDATA\Programs\teveus"
Remove-Item -Recurse "$env:APPDATA\teveus"   # also removes settings and saved keys
```
</details>

## More ways to install

- **Go:** `go install github.com/stawan15/teveus@latest`
- **Manual download:** grab an archive from [Releases](https://github.com/stawan15/teveus/releases)
- **Installer options:** set `TEVEUS_INSTALL_DIR` or `TEVEUS_VERSION=v0.10.0` before running the installer

## Development

```sh
go build -o teveus . && ./teveus   # run from source
go test ./...                       # tests
docs/record.sh                      # re-record the demo GIF and screenshots
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss) and [Glamour](https://github.com/charmbracelet/glamour).
[CLAUDE.md](CLAUDE.md) explains how the code is organised.

## Privacy and security

teveus has no telemetry and no servers: what you send goes only to the AI provider you picked. [PRIVACY.md](PRIVACY.md) lists exactly what is sent where and what is stored on your computer; [SECURITY.md](SECURITY.md) explains the safeguards and how to report a vulnerability privately.

## What's new

See [CHANGELOG.md](CHANGELOG.md) for every version's changes.

## Legal

- teveus is an independent project, not affiliated with, endorsed by, or sponsored by Anthropic PBC or any other AI provider.
  Claude, Claude Code and Anthropic are trademarks of Anthropic PBC; other names belong to their owners and are used only to say what teveus works with.
- Your use of Claude Code and Claude is governed by Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) or [Commercial Terms](https://www.anthropic.com/legal/commercial-terms) and [Usage Policy](https://www.anthropic.com/legal/aup), and other providers' services by their own terms.
  teveus doesn't resell, pay for, or route anyone's AI usage; each person uses their own account or key.
- AI agents can edit and delete files and run commands. Review what you approve and keep your work in version control.
  teveus is provided "as is", without warranty of any kind, and the authors aren't liable for any damage, loss or cost from using it; see the [license](LICENSE).

## License

[MIT](LICENSE)
