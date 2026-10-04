package ui

import (
	"os"
	"regexp"
	"time"
)

// Some terminals write a mouse report in pieces (ESC, then "[<65;28;24M").
// Bubble Tea reads the ESC as the escape key and types the rest into the
// message box. splitKeys holds a report that stops mid-way until the rest
// arrives, so Bubble Tea sees it whole.
const splitWait = 50 * time.Millisecond

var partialReport = regexp.MustCompile(`\x1b(\[(<[0-9;]*)?)?$`)

type splitKeys struct {
	*os.File // keeps Fd, Name and Write, so Bubble Tea still puts the terminal in raw mode
	ready    func(time.Duration) bool
}

func (r splitKeys) Read(p []byte) (int, error) {
	n, err := r.File.Read(p)
	for err == nil && n < len(p) && partialReport.Match(p[:n]) && r.ready(splitWait) {
		var k int
		k, err = r.File.Read(p[n:])
		n += k
	}
	return n, err
}
