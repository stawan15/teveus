//go:build !windows

package agent

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand runs through bash (or sh) in its own process group so an
// interrupt stops the command and everything it spawned.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	sh := "bash"
	if _, err := exec.LookPath(sh); err != nil {
		sh = "sh"
	}
	cmd := exec.CommandContext(ctx, sh, "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd
}
