package ui

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// execInTerminal hands the terminal to cmd, like tea.ExecProcess, but gives
// the command the terminal's own file for stdin.
//
// Bubble Tea passes the program's input reader, which teveus wraps in splitKeys
// (SplitKeysGuard), and that is not an *os.File. os/exec then can't let the
// child inherit the descriptor: it copies from the reader in a goroutine that
// waits for an EOF a terminal never sends, so Run never returns, the finished
// message never arrives, and the app stays frozen on the command's last line
// (the updater and Claude Code's sign-in both did). A real file is inherited
// directly and needs no copy.
func execInTerminal(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
	if cmd.Stdin == nil {
		cmd.Stdin = terminalStdin()
	}
	return tea.ExecProcess(cmd, fn)
}

// terminalStdin is the terminal to hand a foreground command, or nil when
// stdin isn't one. Then stdin was redirected and Bubble Tea reads keys from
// /dev/tty itself, so the command can be given that file as usual.
func terminalStdin() *os.File { return terminalStdinIn(os.Stdin) }

func terminalStdinIn(f *os.File) *os.File {
	if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return f
}
