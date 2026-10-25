package claude

// Backend is an agent engine the UI can drive. The Claude Code CLI (Client)
// is one; internal/agent is teveus's own engine that calls provider
// APIs directly with the user's keys.
type Backend interface {
	Events() <-chan Event
	Send(text string) error
	SendImages(text string, images []Image) error
	Interrupt() error
	SetPermissionMode(mode string) error
	SetModel(model string) error
	Allow(req *PermissionRequest, always bool) error
	Deny(req *PermissionRequest, reason string) error
	Close()
}

// Injector is a backend that can take a message into the turn that is running,
// so it reaches the model at its next step instead of waiting for the turn to
// finish. The Claude Code CLI has no way to do this, so only tooks that drive
// their own loop offer it.
type Injector interface {
	Inject(text string) error
}

// Image is a picture attached to a user turn.
type Image struct {
	MediaType string // image/png, image/jpeg, image/gif or image/webp
	Data      []byte
}

var _ Backend = (*Client)(nil)
