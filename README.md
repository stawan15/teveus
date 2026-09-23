<div align="center">

# teveus

**A friendly terminal for AI coding.**
Use your Claude subscription, or any AI provider with your own key, in one fast, good-looking app.

![teveus fixing a bug: Claude reads the code, proposes a change, you approve it](docs/demo.gif)

</div>

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/stawan15/teveus/main/install.sh | sh
```

Works on macOS and Linux. Run the same line again to update.

> **Using a Claude Pro/Max subscription?** Also install [Claude Code](https://claude.com/claude-code) and log in once.
> teveus uses it behind the scenes.
> For OpenAI, Gemini, OpenRouter and others you don't need anything else.

## Get started

1. Open a terminal in your project folder.
2. Run `teveus`.
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
| 🔌 **Use any AI** | Claude subscription, OpenAI, Anthropic, Gemini, OpenRouter (hundreds of models, many free), Groq, DeepSeek, xAI, Mistral, or local models (Ollama, LM Studio) |
| ↩️ **Undo** | `/undo` puts files back the way they were |
| 🕘 **Pick up later** | `/resume` reopens earlier conversations |
| 💸 **Spend less** | Short, direct answers by default and less text sent per request |
| 🎨 **Make it yours** | 6 themes (including high contrast), 3 guidance levels, reduce motion |

## Keys to know

You only need the first three. Press **`?`** inside teveus to see the rest.

| Key | Does |
|---|---|
| `enter` | Send your message |
| `ctrl+k` | Find any action (command palette) |
| `?` | Show all shortcuts |
| `shift+tab` | Switch mode: **ask first** → **auto-edit** → **plan only** → **autopilot** |
| `@` | Attach a file by name |
| `esc` `esc` | Stop the current task |
| drag with the mouse | Select text (it's copied when you let go) |
| `ctrl+c` `ctrl+c` | Quit |

## Commands

Type `/` to see them all. The most useful:

| Command | Does |
|---|---|
| `/login` | Connect your Claude subscription or another AI provider |
| `/model` | Switch model (search by name, or type `free`) |
| `/undo` | Undo the last change |
| `/resume` | Reopen an earlier conversation |
| `/diff` | See what changed |
| `/init` | Let the AI write notes about your project so it works better |
| `/settings` | All preferences in one place |
| `!command` | Run a shell command, e.g. `!npm test` |

## Connect other AI providers

Run `/login` inside teveus and pick one:

- **OpenRouter**: log in with your browser, no key to copy. Hundreds of models; type `free` in `/model` to find free ones.
- **OpenAI, Anthropic, Gemini, Groq, DeepSeek, xAI, Mistral**: paste an API key. It's checked before saving and stored only on your computer.
- **Ollama or LM Studio**: runs on your own machine; nothing to set up if it's running.

## Questions

<details>
<summary><b>Is my Claude subscription used, or do I pay per token?</b></summary>

With the **Claude Code** engine (the default), teveus uses your Pro/Max subscription through the official `claude` CLI.
With **Direct API** (`/engine`), you pay the provider per token with your own key.
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

```sh
rm ~/.local/bin/teveus
rm -rf ~/.config/teveus   # also removes settings and saved keys
```
</details>

## More ways to install

- **Go:** `go install github.com/stawan15/teveus@latest`
- **Windows or manual download:** grab an archive from [Releases](https://github.com/stawan15/teveus/releases)
- **Installer options:** `TEVEUS_INSTALL_DIR=/usr/local/bin` or `TEVEUS_VERSION=v0.1.1` before `sh`

## Development

```sh
go build -o teveus . && ./teveus   # run from source
go test ./...                       # tests
docs/record.sh                      # re-record the demo GIF from a real session
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss) and [Glamour](https://github.com/charmbracelet/glamour).
[CLAUDE.md](CLAUDE.md) explains how the code is organised.

## License

[MIT](LICENSE)
