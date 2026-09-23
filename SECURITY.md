# Security

## Reporting a vulnerability

Please report security problems privately through GitHub: [open a private report](https://github.com/stawan15/teveus/security/advisories/new). Don't open a public issue for them.

Say what you found, how to reproduce it, and what an attacker could do with it. You'll get a reply within a week. Fixes go into the next release, and reporters are credited unless they'd rather not be.

Only the latest release is supported; please check that the problem still exists there.

## What teveus protects against

An AI agent reads untrusted text (web pages, files, command output) and can act on it. teveus limits what a misled model can do:

- **Approval before acting.** In the default mode, editing files, running commands, fetching or searching the web, and calling MCP tools (except ones the server marks read-only) all ask you first. Modes that ask less (accept-edits, autopilot) are your choice and are shown at all times.
- **The project is the boundary.** Reading or searching outside the folder teveus was started in asks first, in every mode but autopilot. Accept-edits only covers files inside the project. Symlinks are followed before the check, so a link can't lead outside unnoticed.
- **Credentials are off limits.** teveus's file tools refuse, in every mode, to touch its own keys and settings, Claude Code's login, SSH and GPG keys, and common token files (`.aws`, `.netrc`, `.npmrc`, `.docker`, `.kube`, `gh`, `gcloud`).
- **Your rules win.** Deny rules (`/permissions deny …`) apply even in autopilot. "Always allow" for a command is saved as a narrow prefix and never covers a command chained with `&&`, `;`, `|`, `$( )` or redirections.
- **Project MCP servers need approval.** A repository's `.mcp.json` can start programs, so its servers stay off until you approve that exact file with `/mcp`; editing the file withdraws the approval.
- **Private files.** Keys, prompt history, conversations and rules are stored readable only by your user account.
- **Verified installs.** `install.sh` checks the download against the release's SHA-256 checksums.

## Limits

- Commands you approve run with your permissions, and autopilot approves everything except your deny rules. Use it in projects you trust.
- A shell command can read anything your account can; the credential guard covers teveus's file tools, not commands you approve.
- On the Claude Code engine, Claude Code's own permission system applies instead of the above.
