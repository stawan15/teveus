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
	engine := flag.String("engine", "", "agent engine: claude (your installed Claude Code) or api (your own keys: OpenAI, Anthropic, Gemini, OpenRouter, local…); nothing is connected until you choose")
	full := flag.Bool("full", false, "turn off the usage savers (concise answers, lean tools) for this run")
	themeName := flag.String("theme", "", "colour theme: claude, tokyo-night, catppuccin, gruvbox, high-contrast, light")
	showVersion := flag.Bool("version", false, "print the version and exit")
	prompt := flag.String("p", "", "run one prompt without the UI and print the reply (tools needing approval are denied unless -mode allows them)")
	asJSON := flag.Bool("json", false, "with -p: print the reply, cost and token counts as JSON")
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
		Onboard: !settings.Onboarded, AskTrust: !ui.FolderTrusted(opts.Cwd), Version: version, KeyOut: os.Stdout}

	if *prompt != "" {
		cfg.KeyOut = nil
		if err := ui.RunHeadless(cfg, *prompt, *asJSON, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "teveus:", err)
			os.Exit(1)
		}
		return
	}

	progOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if !settings.NoMouse {
		progOpts = append(progOpts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(ui.New(cfg), progOpts...)
	_, err := p.Run()
	fmt.Print("\x1b[=0;1u") // key protocol back to legacy, however the app exited
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
