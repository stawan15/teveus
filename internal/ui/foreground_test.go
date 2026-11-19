package ui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A foreground command has to inherit the terminal's own file. Handed the
// splitKeys wrapper instead, os/exec copies from it in a goroutine that a
// terminal never ends with EOF, and the command never finishes.
func TestExecInTerminalKeepsTheCallersStdin(t *testing.T) {
	in := strings.NewReader("input the caller chose")
	cmd := exec.Command("true")
	cmd.Stdin = in
	_ = execInTerminal(cmd, nil)
	if cmd.Stdin != in {
		t.Fatalf("Stdin = %v, want the reader the caller set", cmd.Stdin)
	}
}

func TestTerminalStdinOnlyTakesATerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no", os.DevNull)
	}
	defer devNull.Close()
	if got := terminalStdinIn(devNull); got != devNull {
		t.Fatalf("a character device should be handed over, got %v", got)
	}

	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got := terminalStdinIn(file); got != nil {
		t.Fatalf("redirected stdin should be left to Bubble Tea, got %v", got)
	}
}
