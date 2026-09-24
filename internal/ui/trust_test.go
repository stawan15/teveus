package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

func TestFolderTrust(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	root := t.TempDir()
	inner := filepath.Join(root, "a", "b")
	os.MkdirAll(inner, 0o755)
	if FolderTrusted(root) {
		t.Fatal("a new folder was trusted")
	}
	trustFolder(root)
	if !FolderTrusted(root) || !FolderTrusted(inner) {
		t.Fatal("a trusted folder should cover the folders inside it")
	}
	if FolderTrusted(root + "-other") {
		t.Fatal("a folder that only shares a name prefix was trusted")
	}
	if fi, err := os.Stat(trustPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("trust file: %v %v", fi, err)
	}
}

func TestUntrustedFolderAsksFirst(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	cwd := t.TempDir()
	m := New(Config{Dark: true, AskTrust: true, Onboard: true, Engine: "api", Claude: claude.Options{Cwd: cwd}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Init()
	if m.client != nil || m.pop.title != "Do you trust this folder?" {
		t.Fatalf("started without asking (popup %q)", m.pop.title)
	}
	if v := strings.Join(strings.Fields(ansi.Strip(m.View())), " "); !strings.Contains(v, "Yes, I trust this folder") || !strings.Contains(v, "No, exit") {
		t.Fatalf("question not shown:\n%s", v)
	}
	// First-run setup waits for the answer, then opens.
	m.Update(claudeAuthMsg{})
	if m.pop.title != "Do you trust this folder?" {
		t.Fatalf("setup jumped in: %q", m.pop.title)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !FolderTrusted(cwd) || !strings.HasPrefix(m.pop.title, "Welcome to teveus") {
		t.Fatalf("trusted=%v popup=%q", FolderTrusted(cwd), m.pop.title)
	}
}

func TestDecliningTrustExits(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	cwd := t.TempDir()
	m := New(Config{Dark: true, AskTrust: true, Claude: claude.Options{Cwd: cwd}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Init()
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || cmd() != tea.Quit() || FolderTrusted(cwd) {
		t.Fatal("declining should quit and remember nothing")
	}
}
