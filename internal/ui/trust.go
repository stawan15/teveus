package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The first time teveus opens in a folder it asks whether to trust it, because
// the AI can read the files there and, with approval, change them and run
// commands. The answer is kept in trusted-folders.json; a trusted folder
// covers the folders inside it.

func trustPath() string { return filepath.Join(configDir(), "trusted-folders.json") }

func folderKey(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir
}

// FolderTrusted reports whether dir, or a folder above it, was trusted.
func FolderTrusted(dir string) bool {
	var list []string
	if b, err := os.ReadFile(trustPath()); err == nil {
		json.Unmarshal(b, &list)
	}
	dir = folderKey(dir)
	for _, t := range list {
		if dir == t || strings.HasPrefix(dir, strings.TrimSuffix(t, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func trustFolder(dir string) {
	var list []string
	if b, err := os.ReadFile(trustPath()); err == nil {
		json.Unmarshal(b, &list)
	}
	b, _ := json.MarshalIndent(append(list, folderKey(dir)), "", "  ")
	writePrivate("trusted-folders.json", b)
}

// askTrust opens the question; start runs once the folder is trusted.
func (m *Model) askTrust() {
	m.openPicker("Do you trust this folder?", []choice{
		{label: "Yes, I trust this folder", color: cGreen, desc: "don't ask again here", run: func(m *Model) tea.Cmd {
			trustFolder(m.cwd)
			m.cfg.AskTrust = false
			cmd := m.start(m.cfg.Claude)
			m.startSetup()
			return cmd
		}},
		{label: "No, exit", color: cDim, desc: "nothing is read or started", run: func(m *Model) tea.Cmd { return m.quit() }},
	}, 0)
	m.pop.body = []string{
		"",
		sAccent.Render("  " + m.cwd),
		"",
		sText.Render("  teveus can read the files in this folder. With your approval it can also"),
		sText.Render("  change them and run commands. Only trust folders you got from a source you trust."),
	}
	m.pop.accent = cYellow
	m.pop.flat = true
	m.pop.cancel = func(m *Model) { m.quit() }
}

// startSetup opens first-run setup once nothing else is waiting on the user.
func (m *Model) startSetup() {
	if m.cfg.Onboard && !m.settings.Onboarded && m.authChecked && !m.pop.open() && len(m.perms) == 0 {
		m.cfg.Onboard = false
		m.startOnboarding()
	}
}
