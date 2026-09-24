// Package agent is teveus's own coding agent: it calls provider APIs
// directly (Anthropic, or any OpenAI-compatible API such as OpenAI, Gemini,
// OpenRouter, Groq, DeepSeek, xAI, Mistral, Ollama, LM Studio), runs the
// tools itself, and speaks the same claude.Event protocol as the Claude Code
// engine so the UI treats both alike.
package agent

import (
	"context"
	"encoding/json"

	"github.com/stawan15/teveus/internal/claude"
)

// Protocol is the wire format a provider speaks.
type Protocol string

const (
	Anthropic Protocol = "anthropic"
	OpenAI    Protocol = "openai" // Chat Completions, as implemented by most providers
)

type Provider struct {
	ID       string
	Name     string
	Protocol Protocol
	BaseURL  string
	EnvKeys  []string // environment variables checked for a key
	KeyURL   string   // where to create a key
	NoKey    bool     // local servers that need no key
	Browser  bool     // supports browser login (OpenRouter PKCE)
	Custom   bool     // base URL supplied by the user
}

var Providers = []Provider{
	{ID: "anthropic", Name: "Anthropic", Protocol: Anthropic, BaseURL: "https://api.anthropic.com/v1",
		EnvKeys: []string{"ANTHROPIC_API_KEY"}, KeyURL: "https://console.anthropic.com/settings/keys"},
	{ID: "openai", Name: "OpenAI", Protocol: OpenAI, BaseURL: "https://api.openai.com/v1",
		EnvKeys: []string{"OPENAI_API_KEY"}, KeyURL: "https://platform.openai.com/api-keys"},
	{ID: "google", Name: "Google Gemini", Protocol: OpenAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		EnvKeys: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, KeyURL: "https://aistudio.google.com/apikey"},
	{ID: "openrouter", Name: "OpenRouter", Protocol: OpenAI, BaseURL: "https://openrouter.ai/api/v1",
		EnvKeys: []string{"OPENROUTER_API_KEY"}, KeyURL: "https://openrouter.ai/keys", Browser: true},
	{ID: "groq", Name: "Groq", Protocol: OpenAI, BaseURL: "https://api.groq.com/openai/v1",
		EnvKeys: []string{"GROQ_API_KEY"}, KeyURL: "https://console.groq.com/keys"},
	{ID: "deepseek", Name: "DeepSeek", Protocol: OpenAI, BaseURL: "https://api.deepseek.com/v1",
		EnvKeys: []string{"DEEPSEEK_API_KEY"}, KeyURL: "https://platform.deepseek.com/api_keys"},
	{ID: "xai", Name: "xAI", Protocol: OpenAI, BaseURL: "https://api.x.ai/v1",
		EnvKeys: []string{"XAI_API_KEY"}, KeyURL: "https://console.x.ai"},
	{ID: "mistral", Name: "Mistral", Protocol: OpenAI, BaseURL: "https://api.mistral.ai/v1",
		EnvKeys: []string{"MISTRAL_API_KEY"}, KeyURL: "https://console.mistral.ai/api-keys"},
	{ID: "ollama", Name: "Ollama (local)", Protocol: OpenAI, BaseURL: "http://localhost:11434/v1", NoKey: true},
	{ID: "lmstudio", Name: "LM Studio (local)", Protocol: OpenAI, BaseURL: "http://localhost:1234/v1", NoKey: true},
	{ID: "custom", Name: "Custom (OpenAI-compatible)", Protocol: OpenAI, Custom: true},
}

func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Message is the provider-neutral conversation format.
type Message struct {
	Role       string          // "user", "assistant" or "tool"
	Text       string          // user/assistant text
	Images     []claude.Image  `json:",omitempty"` // user: pictures sent with the text
	ToolCalls  []ToolCall      // assistant
	Reasoning  json.RawMessage `json:",omitempty"` // assistant: opaque reasoning state to send back (OpenRouter reasoning_details)
	ToolCallID string          // tool
	Result     string          // tool
	IsError    bool            // tool
	Pruned     bool            `json:",omitempty"` // tool: the result is sent as a short note (prune.go); Result keeps the original
}

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
	// Extra holds provider-specific fields of the call (e.g. Gemini's thought
	// signature in extra_content) that must be echoed back unchanged.
	Extra map[string]json.RawMessage
}

type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []ToolDef
	Effort   string // low, medium, high, xhigh, max; "" leaves the model's default
}

// Chunk is one streamed piece of a model response.
type Chunk struct {
	Text      string    // text delta
	Thinking  bool      // model is reasoning
	ToolStart *ToolCall // a tool call began (name known, input pending)
	Done      *Response // final
}

type Response struct {
	Text      string
	ToolCalls []ToolCall
	Reasoning json.RawMessage
	Usage     Usage
	Stop      string
}

type Usage struct {
	Input, Output, CacheRead, CacheWrite int
	Cost                                 float64 // when the provider reports it
}

// Model is one entry of a provider's model list.
type Model struct {
	ID      string
	Name    string
	Context int     // tokens, when the provider says
	In, Out float64 // USD per million tokens, when the provider says
	Priced  bool
	Created int64 // unix time, when the provider says; newer sorts first
}

// Client streams completions from one provider.
type Client interface {
	Stream(ctx context.Context, req Request, out func(Chunk)) (*Response, error)
	Models(ctx context.Context) ([]Model, error)
}

func NewClient(p Provider, cred Credential) Client {
	base := p.BaseURL
	if cred.BaseURL != "" {
		base = cred.BaseURL
	}
	if p.Protocol == Anthropic {
		return &anthropicClient{base: base, key: cred.Key}
	}
	return &openaiClient{base: base, key: cred.Key, provider: p.ID}
}
