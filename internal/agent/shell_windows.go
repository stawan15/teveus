//go:build windows

package agent

import (
	"context"
	"os/exec"
)

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/C", command)
}
