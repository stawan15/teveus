//go:build !windows

package ui

import (
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// SplitKeysGuard wraps the terminal's input so a key or mouse report that
// arrives in pieces isn't typed as text. It returns false when stdin isn't a
// terminal, where Bubble Tea opens /dev/tty itself.
func SplitKeysGuard() (io.Reader, bool) {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil, false
	}
	fd := int(os.Stdin.Fd())
	ready := func(d time.Duration) bool {
		var set unix.FdSet
		set.Set(fd)
		tv := unix.NsecToTimeval(int64(d))
		n, err := unix.Select(fd+1, &set, nil, nil, &tv)
		return err == nil && n > 0
	}
	return &splitKeys{File: os.Stdin, ready: ready}, true
}
