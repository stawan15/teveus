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
	held     []byte // the start of a report that a full buffer cut off, returned first next time
}

func (r *splitKeys) Read(p []byte) (int, error) {
	n, err := copy(p, r.held), error(nil)
	r.held = nil
	if n == 0 {
		n, err = r.File.Read(p)
	}
	for err == nil && n < len(p) && partialReport.Match(p[:n]) && r.ready(splitWait) {
		var k int
		k, err = r.File.Read(p[n:])
		n += k
	}
	// A busy UI leaves a backlog, so reads fill Bubble Tea's buffer and can
	// end mid-report. There is no room to read on: keep the piece for later.
	if loc := partialReport.FindIndex(p[:n]); err == nil && n == len(p) && loc != nil && loc[0] > 0 {
		r.held = append(r.held, p[loc[0]:n]...)
		n = loc[0]
	}
	return n, err
}
