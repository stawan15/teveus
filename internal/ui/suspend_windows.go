package ui

// SuspendGuard does nothing on Windows, which has no ctrl+z suspend.
func SuspendGuard(release func()) (stop func()) { return func() {} }
