//go:build windows

package agent

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd", "/C", command)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		if err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}
