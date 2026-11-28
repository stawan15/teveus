//go:build !windows

package agent

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// shellCommand runs through bash (or sh) in its own process group so an
// interrupt stops the command and everything it spawned. It gets childEnv:
// a command the model wrote can read the project, not the user's keys.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	sh := "bash"
	if _, err := exec.LookPath(sh); err != nil {
		sh = "sh"
	}
	cmd := exec.CommandContext(ctx, sh, "-c", command)
	cmd.Env = childEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Do not block indefinitely waiting for stdout/stderr pipes if a grandchild
	// process inherited the file descriptors and survived SIGKILL.
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd
}
