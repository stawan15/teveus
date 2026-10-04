package ui

import "io"

// SplitKeysGuard does nothing on Windows, where Bubble Tea reads console
// events instead of escape sequences.
func SplitKeysGuard() (io.Reader, bool) { return nil, false }
