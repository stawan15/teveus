# Security

## Reporting a vulnerability

Please report security problems privately through GitHub: [open a private report](https://github.com/stawan15/teveus/security/advisories/new). Don't open a public issue for them.

Say what you found, how to reproduce it, and what an attacker could do with it. You'll get a reply within a week. Fixes go into the next release, and reporters are credited unless they'd rather not be.

Only the latest release is supported; please check that the problem still exists there.

## What teveus protects against

An AI agent reads untrusted text (web pages, files, command output) and can act on it. teveus limits what a misled model can do:

- **Approval before acting.** In the default mode, editing files, running commands, fetching or searching the web, and calling MCP tools (except ones the server marks read-only) all ask you first. Modes that ask less (accept-edits, autopilot) are your choice and are shown at all times.
- **The project is the boundary.** Reading or searching outside the folder teveus was started in asks first, in every mode but autopilot. Accept-edits only covers files inside the project. Symlinks are followed before the check, so a link can't lead outside unnoticed.
- **Credentials are off limits.** teveus's file tools refuse, in every mode, to touch its own keys and settings, Claude Code's login, SSH and GPG keys, and common token files (`.aws`, `.netrc`, `.npmrc`, `.docker`, `.kube`, `gh`, `gcloud`). A shell command that names one of those paths (or a cloud metadata address) asks first even in autopilot, because a command has no paths to inspect before it runs.
- **Child processes don't inherit your keys.** Commands, background jobs, hooks and MCP servers get a scrubbed environment: `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `AWS_*` and similar are dropped. A model that runs `env` sees your `PATH`, not your credentials. An MCP server still gets exactly the variables its own `.mcp.json` entry gives it.
- **WebFetch can't reach this machine.** A URL the model chose is untrusted input, so WebFetch refuses loopback, private, link-local and CGNAT addresses and cloud metadata (`169.254.169.254`), on the first request and on every redirect, and checks the address it actually connected to so a DNS answer can't change in between. `WebFetchLocal` in settings turns that off for a local dev server.
- **Secrets are masked in what's stored and shown.** Conversations saved to disk, prompt history, the exported transcript and the phone page pass through a redactor: known key shapes (`sk-`, `ghp_`, `AKIA`, PEM blocks, `Bearer …`, `user:password@`, `password=`), the keys in `auth.json` and the phone token are replaced with `[redacted:…abcd]`, keeping only the last four characters so you can tell which key it was. `/scrub` cleans conversations saved before this existed.
- **Your rules win.** Deny rules (`/permissions deny …`) apply even in autopilot. "Always allow" for a command is saved as a narrow prefix and never covers a command chained with `&&`, `;`, `|`, `$( )` or redirections.
- **New folders ask first.** The first time teveus opens in a folder it asks whether you trust it, and reads or starts nothing until you say yes. Trusting a folder also covers the folders inside it.
- **Project MCP servers need approval.** A repository's `.mcp.json` can start programs, so its servers stay off until you approve that exact file with `/mcp`; editing the file withdraws the approval.
- **Hooks stay out of repositories.** Hooks run commands on your computer, so they are read only from teveus's own settings folder (`hooks.json`), which the file tools can't touch. A repository can add slash commands and skills, but those are prompts, like `CLAUDE.md`: they run nothing themselves and the model's tool calls still ask as usual.
- **Private files.** Keys, prompt history, conversations and rules are stored readable only by your user account.
- **The phone page is off by default, and token-only.** Turning on `/settings → Phone page` (or passing `-phone`) starts a small web server that lets a phone prompt and approve. It listens on `127.0.0.1:8787` unless you name another address, and warns when that address is reachable from other machines. It answers nothing without the random token in the link teveus prints (kept in `phone-token` in the config dir, so the link stays the same between runs). Opening the link trades the token for an `HttpOnly`, `SameSite=Strict` cookie and redirects to `/`, so the URL bar and the page's own scripts no longer carry it. It accepts answers only as POSTs, sends `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `nosniff` and a nonce-based Content-Security-Policy, and bounds request size, headers and timeouts. It never holds keys or opens conversations: it asks the running session through the UI, so a page can't reach anything the keyboard couldn't. The page can make permissions stricter but never looser, and can't grant an "always allow" rule — those stay in the terminal. A page lists the other running teveus windows from small handle files in the config dir and can open one, but never sees another window's token.
- **Verified installs and updates.** `install.sh` and `install.ps1` check the download against the release's SHA-256 checksums, and an update from inside teveus runs the same installer, pinned to the exact version it offered. teveus never updates without asking.

## Limits

- Commands you approve run with your permissions, and autopilot approves everything except your deny rules. Use it in projects you trust.
- Hooks and background commands run with your permissions. A background command you approved keeps running until it ends, you stop it, or its session closes.
- A shell command can still read anything your account can if you approve it; the credential guard asks first and the scrub keeps keys out of the files, but it can't tell a legitimate `cat` from a hostile one.
- The redactor catches the shapes it knows and the exact keys teveus itself holds. A secret with no recognisable shape that you haven't stored in teveus can still end up in a conversation you save; review before sharing an export.
- Anyone who has the phone link can prompt and approve while the page is on, and the page is plain HTTP unless you put it behind a TLS tunnel. The link is the only credential, so treat it like a password: use it on a network you trust (home Wi-Fi, or a Tailscale address) and turn it off when you're done.
- On the Claude Code engine, Claude Code's own permission system applies instead of the above.
