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
	phoneAddr := flag.String("phone", "", "also serve a page a phone can prompt and approve from, e.g. -phone 0.0.0.0:8787 (off by default)")
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
		Onboard: !settings.Onboarded, AskTrust: !ui.FolderTrusted(opts.Cwd), Version: version, KeyOut: os.Stdout, PhoneAddr: *phoneAddr}

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
	if in, ok := ui.SplitKeysGuard(); ok {
		progOpts = append(progOpts, tea.WithInput(in))
	}
	// The phone page hands messages to the program, which doesn't exist yet.
	var send func(tea.Msg)
	cfg.Send = func(msg tea.Msg) {
		if send != nil {
			send(msg)
		}
	}
	model := ui.New(cfg)
	p := tea.NewProgram(model, progOpts...)
	send = p.Send
	// A stop signal that didn't come from ctrl+z (SIGTSTP from elsewhere, or
	// SIGTTIN/SIGTTOU from a background group) would stop the process with the
	// terminal still in the alternate screen and mouse capture still on.
	stopSuspend := ui.SuspendGuard(model, p)
	_, err := p.Run()
	stopSuspend()
	fmt.Print("\x1b[=0;1u") // key protocol back to legacy, however the app exited
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// An update replaces the binary under us, so the app stops rather than
	// carrying on in a process whose terminal has been handed to an installer
	// and back. Say what to do now that we are back in a normal shell.
	if v := model.Updated(); v != "" {
		fmt.Println("✓ teveus " + v + " is installed. Start it again to use it.")
	}
}
