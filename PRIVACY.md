# Privacy

teveus is a program that runs on your computer. It has no servers, no accounts, and no telemetry: it doesn't collect analytics, crash reports or usage data, and it doesn't check for updates in the background. The author receives nothing from your use of it.

What does leave your computer goes to services **you** choose, under **their** terms.

## What is sent, and to whom

| When | What is sent | To |
|---|---|---|
| You use the **Claude Code** engine | Your messages, and whatever Claude Code reads or runs for them | Anthropic, by your installed Claude Code, under [Anthropic's terms and privacy policy](https://www.anthropic.com/legal/privacy). teveus only starts the `claude` program; it never sees or stores your Claude login. |
| You use the **Direct API** engine | Your messages, images you attach, and the contents of files and command output the model reads while working | The AI provider whose model you picked (OpenAI, Anthropic, Google, OpenRouter, …), under that provider's terms. Local models (Ollama, LM Studio) stay on your machine. |
| teveus starts on the Direct API engine | A request for the list of models | Each provider you've connected |
| The model uses **WebFetch** (you approve it) | A request for that web address | That website |
| The model uses **WebSearch** (you approve it) | The search words | Brave Search or Tavily, whichever key you connected |
| The model uses an **MCP** tool | Whatever that tool is given | The MCP server you configured (a program on your computer, or the address you set) |
| You log in to **OpenRouter** in the browser | A one-time login exchange | openrouter.ai |
| You install or update with `install.sh` | A download request | GitHub |

Nothing else is sent. Your clipboard is only read when you press `ctrl+v`, and only written when you copy.

## What is stored on your computer

Everything lives in `~/.config/teveus/` (on Windows, `%AppData%\teveus\`; `TEVEUS_CONFIG` moves it). Files are readable only by your user account.

| File | Contents | Kept |
|---|---|---|
| `auth.json` | API keys you saved with `/login` | Until you `/logout` |
| `settings.json` | Your preferences | Always |
| `history.json` | Your last 500 prompts, for the ↑ key | Until `/purge` |
| `sessions/` | Direct API conversations (text and images), for `/resume` | 30 days, then deleted (`"keepSessionsDays"` in settings; `-1` keeps them) |
| `permissions.json` | Allow/deny rules you saved | Until you remove them (`/permissions`) |
| `mcp-approved.json` | Which projects' MCP servers you approved | Always |

`/purge` deletes the saved conversations and prompt history at once. Deleting the folder removes everything, including keys. Claude Code keeps its own history in `~/.claude/`; teveus doesn't manage it.

teveus's own tools never read `auth.json` or other credential files (SSH keys, cloud credentials, Claude Code's login), even if a model asks them to.

## Changes

If a future version sends anything new, this page will say so before that version ships.
