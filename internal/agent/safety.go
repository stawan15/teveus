package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// File tools are confined in two ways, whatever the permission mode:
//
//   - Credential stores are off limits: teveus's own config (API keys,
//     permission rules, MCP approvals), Claude Code's login, SSH and GPG keys,
//     and the usual token files. A web page the model reads could try to
//     talk it into sending one of these somewhere.
//   - Paths outside the project always need the user's OK, even for reads
//     and even in accept-edits mode; autopilot is the only exception.
//
// Bash can still read anything the user can; it is asked for in every mode
// except autopilot, which the user chooses knowingly.

// sensitive reports whether p is, or is inside, a credential store. Reading
// a skill the user wrote is fine; writing one is not, because a skill is
// instructions the model will follow later, loaded without asking.
func (e *Engine) sensitivePath(p string, writing bool) bool {
	home, _ := os.UserHomeDir()
	var roots []string
	if e.opts.ConfigDir != "" {
		roots = append(roots, e.opts.ConfigDir)
	}
	if home != "" {
		for _, r := range []string{
			".ssh", ".gnupg", ".aws/credentials", ".aws/config", ".netrc", ".git-credentials",
			".npmrc", ".pypirc", ".docker/config.json", ".kube/config", ".config/gh/hosts.yml",
			".claude/.credentials.json", ".config/gcloud/credentials.db", ".config/gcloud/application_default_credentials.json",
			".local/share/opencode/auth.json",
		} {
			roots = append(roots, filepath.Join(home, r))
		}
	}
	var readable []string
	if !writing && e.opts.ConfigDir != "" {
		readable = append(readable, filepath.Join(e.opts.ConfigDir, "skills"), filepath.Join(e.opts.ConfigDir, "commands"))
	}
	for _, cand := range realPaths(p) {
		// The user's own skills and commands are prompts they wrote for the
		// model to read; the rest of the config directory holds keys and rules.
		if withinAny(cand, readable) {
			continue
		}
		for _, r := range roots {
			for _, rr := range realPaths(r) {
				if within(cand, rr) {
					// Public SSH keys are fine to read.
					return !(strings.HasSuffix(cand, ".pub") && strings.Contains(cand, string(filepath.Separator)+".ssh"+string(filepath.Separator)))
				}
			}
		}
	}
	return false
}

// withinAny reports whether p is inside any of roots.
func withinAny(p string, roots []string) bool {
	for _, r := range roots {
		for _, rr := range realPaths(r) {
			if within(p, rr) {
				return true
			}
		}
	}
	return false
}

// inProject reports whether p is inside the working directory, symlinks
// resolved, so a link in the project can't lead outside it unnoticed.
func (e *Engine) inProject(p string) bool {
	return within(realPath(p), realPath(e.opts.Cwd))
}

// realPaths returns p and, when it differs, p with symlinks resolved. A
// path that doesn't exist yet is resolved through its nearest existing
// parent.
func realPaths(p string) []string {
	p = filepath.Clean(p)
	r := realPath(p)
	if r == p {
		return []string{p}
	}
	return []string{p, r}
}

func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

func within(p, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// toolPaths returns the files or directories a file tool call touches.
func (e *Engine) toolPaths(name string, in map[string]any) []string {
	switch name {
	case "Read", "Write", "Edit", "NotebookEdit":
		return []string{e.box.abs(editPath(in))}
	case "Grep", "Glob":
		return []string{e.box.abs(str(in, "path"))}
	}
	return nil
}

// editPath is the file a Read, Write, Edit or NotebookEdit call is about.
func editPath(in map[string]any) string {
	if p := str(in, "file_path"); p != "" {
		return p
	}
	return str(in, "notebook_path")
}

// secretCommandMarkers are substrings that say a shell command is reaching for
// a credential store. Bash can read anything the user can, so this is only a
// list of things that always warrant a question — the file tools refuse them
// outright, but a shell has no paths to inspect before it runs.
var secretCommandMarkers = []string{
	".ssh/", ".ssh ", "id_rsa", "id_ed25519", "id_ecdsa",
	".gnupg", ".aws/credentials", ".aws/config", ".netrc", ".git-credentials",
	".npmrc", ".pypirc", ".docker/config.json", ".kube/config",
	".claude/.credentials.json", ".config/gh/hosts.yml",
	".config/gcloud", ".local/share/opencode/auth.json",
	"auth.json", "phone-token", "mcp-approved.json",
	"security-credentials", "169.254.169.254",
}

// commandTouchesSecrets reports whether a shell command mentions one of the
// credential stores, or teveus's own config directory.
func (e *Engine) commandTouchesSecrets(cmd string) bool {
	l := strings.ToLower(cmd)
	if e.opts.ConfigDir != "" {
		dir := strings.ToLower(e.opts.ConfigDir)
		if strings.Contains(l, dir) {
			return true
		}
		// ~/.config/teveus written as $HOME or ~ is caught by the markers below.
	}
	for _, m := range secretCommandMarkers {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}
