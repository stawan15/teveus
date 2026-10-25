//go:build windows

package ui

import "os"

// processAlive reports whether a pid still names a running process. Windows
// has no signal-0 check, so open the process instead; a pid we may not open
// reads as alive, which only means a stale handle survives until its time
// runs out.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
