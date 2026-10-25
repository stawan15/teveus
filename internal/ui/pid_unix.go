//go:build !windows

package ui

import "syscall"

// processAlive reports whether a pid still names a running process. Signal 0
// checks without touching it; a process we may not signal reads as alive,
// which only means a stale handle survives until its time runs out.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil || syscall.Kill(pid, 0) == syscall.EPERM
}
