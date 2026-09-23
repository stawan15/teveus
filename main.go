package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stawan15/teveus/internal/claude"
	"github.com/stawan15/teveus/internal/ui"
)

// version is set at release time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	var opts claude.Options
	flag.StringVar(&opts.Model, "model", "", "model alias or ID (e.g. opus, sonnet, haiku)")
	flag.StringVar(&opts.PermissionMode, "mode", "", "permission mode: default, acceptEdits, plan, auto")
	flag.StringVar(&opts.Resume, "resume", "", "resume a session by ID")
	flag.BoolVar(&opts.Continue, "c", false, "continue the most recent session in this directory")
	flag.StringVar(&opts.Binary, "claude", "claude", "path to the claude CLI")
	engine := flag.String("engine", "", "agent engine: claude (Claude Code, default) or api (your own keys: OpenAI, Anthropic, Gemini, OpenRouter, local…)")
	full := flag.Bool("full", false, "turn off the usage savers (concise answers, lean tools) for this run")
	themeName := flag.String("theme", "", "colour theme: claude, tokyo-night, catppuccin, gruvbox, high-contrast, light")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if version == "dev" {
		// go install …@v0.1.0 records the module version in the binary.
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = strings.TrimPrefix(bi.Main.Version, "v")
		}
	}
	if *showVersion {
		fmt.Println("teveus", version)
		return
	}

	opts.Cwd, _ = os.Getwd()
	settings := ui.LoadSettings()
	cfg := ui.Config{Claude: opts, Dark: lipgloss.HasDarkBackground(), Theme: *themeName, Settings: settings, Full: *full, Engine: *engine,
		Onboard: !settings.Onboarded, Version: version}

	progOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if !settings.NoMouse {
		progOpts = append(progOpts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(ui.New(cfg), progOpts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
