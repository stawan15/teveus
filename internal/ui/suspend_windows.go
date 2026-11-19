package ui

// SuspendGuard does nothing on Windows, which has no ctrl+z suspend.
func SuspendGuard(*Model, Suspender) (stop func()) { return func() {} }
