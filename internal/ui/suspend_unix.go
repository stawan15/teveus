//go:build !windows

package ui

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// stopSignals are the signals that stop the process without teveus asking for
// it. SIGTTIN and SIGTTOU arrive when a background process group reads or
// writes the terminal. They are the ones that used to wreck the screen: the
// process stopped with the alternate screen, the key protocol and mouse
// capture still on, so the shell echoed every mouse report as text.
var stopSignals = []os.Signal{syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU}

// ttyStops is the subset whose way back belongs to this guard. ctrl+z also
// raises a SIGTSTP (Bubble Tea sends the group one after releasing the
// terminal), and Bubble Tea restores the terminal itself after fg, so a stop
// from ctrl+z must not be released twice.
var ttyStops = map[os.Signal]bool{syscall.SIGTTIN: true, syscall.SIGTTOU: true}

// SuspendGuard restores the terminal before the process stops for a signal it
// didn't ask for, and takes it back when the process continues.
//
// In raw mode ctrl+z arrives as a keystroke (handled in model.go, so Bubble Tea
// releases the terminal and restores it on the way back), but the kernel can
// stop the process directly: SIGTSTP from `kill -TSTP`, a closing terminal or a
// terminal that isn't in raw mode, and SIGTTIN or SIGTTOU from a background
// process group. Left alone the process stops with the alternate screen and
// mouse capture still on, and (fg) returns to a shell drawing into a screen
// that was never restored.
//
// The signal is re-raised with its handler removed, so the default behaviour
// (stopping the process) still happens and the shell is not left waiting.
func SuspendGuard(m *Model, p Suspender) (stop func()) {
	sig := make(chan os.Signal, 4)
	arm := func() { signal.Notify(sig, append([]os.Signal{syscall.SIGCONT}, stopSignals...)...) }
	arm()

	var stopped atomic.Bool
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case s := <-sig:
				if s == syscall.SIGCONT {
					// fg: the terminal is ours again. Take it back only when
					// this guard is what gave it up; after ctrl+z Bubble Tea
					// restores the terminal on its own.
					if stopped.CompareAndSwap(true, false) {
						_ = p.RestoreTerminal()
						p.Send(tea.ResumeMsg{})
					}
					// The stopping signal's handler was reset, so put it back
					// for the next stop.
					arm()
					continue
				}
				// Put the key protocol back to legacy and let the terminal go
				// before the shell draws on it.
				m.setKittyKeys(false)
				_ = p.ReleaseTerminal()
				if ttyStops[s] {
					stopped.Store(true)
				}
				// Hand the signal back to its default action, once.
				signal.Reset(s)
				_ = syscall.Kill(os.Getpid(), s.(syscall.Signal))
			}
		}
	}()
	return func() {
		signal.Stop(sig)
		close(done)
	}
}
