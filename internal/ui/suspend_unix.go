//go:build !windows

package ui

import (
	"os"
	"os/signal"
	"syscall"
)

// SuspendGuard restores the terminal before a SIGTSTP that didn't come from
// ctrl+z. In raw mode ctrl+z arrives as a keystroke (handled in model.go, so
// Bubble Tea releases the terminal itself), but the signal can still arrive
// from `kill -TSTP`, a closing terminal, or a terminal that isn't in raw mode.
// Left alone it stops the process with the alternate screen and the key
// protocol still on, and (fg) returns to a shell drawing into a screen that was
// never restored.
//
// release must put the terminal back; after calling it the signal is re-raised
// with the handler removed, so the default behaviour (stopping the process)
// still happens and the shell is not left waiting.
func SuspendGuard(release func()) (stop func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTSTP)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sig:
				release()
				// Hand the signal back to its default action, once.
				signal.Reset(syscall.SIGTSTP)
				syscall.Kill(os.Getpid(), syscall.SIGTSTP)
				return
			}
		}
	}()
	return func() {
		signal.Stop(sig)
		close(done)
	}
}
