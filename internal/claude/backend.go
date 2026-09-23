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

// Image is a picture attached to a user turn.
type Image struct {
	MediaType string // image/png, image/jpeg, image/gif or image/webp
	Data      []byte
}

var _ Backend = (*Client)(nil)
