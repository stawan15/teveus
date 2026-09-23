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

// sensitive reports whether p is, or is inside, a credential store.
func (e *Engine) sensitive(p string) bool {
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
	for _, cand := range realPaths(p) {
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
	case "Read", "Write", "Edit":
		return []string{e.box.abs(str(in, "file_path"))}
	case "Grep", "Glob":
		return []string{e.box.abs(str(in, "path"))}
	}
	return nil
}
