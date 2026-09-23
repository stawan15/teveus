package claude

import (
	"encoding/json"
	"strings"
)

// Event is anything decoded from the CLI's stdout.
type Event interface{ isEvent() }

type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
}

type ModelInfo struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`

	// Optional presentation hints from the API engine.
	Group string `json:"-"` // e.g. "OpenRouter · Anthropic"
	Meta  string `json:"-"` // e.g. "200k · $3/$15"
	Free  bool   `json:"-"`
}

// Ready is the answer to our initialize request.
type Ready struct {
	Commands       []Command
	Models         []ModelInfo
	PermissionMode string
}

// Init is the system/init message sent at the start of each turn.
type Init struct {
	SessionID      string
	Model          string
	Cwd            string
	PermissionMode string
	Tools          []string
}

// Status reports a CLI state change such as a permission mode switch.
type Status struct {
	Status         string
	PermissionMode string
}

// BlockStart marks the start of a streamed content block ("text", "thinking", "tool_use").
type BlockStart struct {
	Kind     string
	ToolName string
}

// TextDelta is a chunk of streamed assistant text.
type TextDelta struct{ Text string }

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ResultText flattens a tool_result's content, which is either a string or
// a list of content blocks.
func (b ContentBlock) ResultText() string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var parts []ContentBlock
	if json.Unmarshal(b.Content, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Type == "text" {
				out = append(out, p.Text)
			} else if p.Type != "" {
				out = append(out, "["+p.Type+"]")
			}
		}
		return strings.Join(out, "\n")
	}
	return string(b.Content)
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Context is the prompt size the model saw for this request.
func (u Usage) Context() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// AssistantMessage carries completed assistant content blocks. Parent is set
// when the message comes from a subagent.
type AssistantMessage struct {
	Parent string
	Blocks []ContentBlock
	Usage  *Usage
}

// ToolResults carries tool_result blocks returned to the model.
type ToolResults struct {
	Parent  string
	Results []ContentBlock
}

type PermissionRequest struct {
	RequestID   string
	ToolName    string
	ToolUseID   string
	Description string
	Input       json.RawMessage
	Suggestions json.RawMessage
}

type Window struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

type RateLimit struct {
	Status   string
	FiveHour *Window
	SevenDay *Window
}

// Result ends a turn.
type Result struct {
	Subtype    string
	IsError    bool
	Text       string
	CostUSD    float64
	DurationMS int64
	NumTurns   int
	Usage      Usage
}

// ControlResult answers one of our control requests. Tag is the prefix of
// the request ID ("mode", "model", ...).
type ControlResult struct {
	Tag   string
	Error string
	Body  json.RawMessage
}

type Exited struct {
	Err       error
	Stderr    string
	Requested bool // we closed it ourselves
}

// Unknown lines are surfaced so nothing is silently lost while debugging.
type Unknown struct{ Line string }

func (Ready) isEvent()              {}
func (Init) isEvent()               {}
func (Status) isEvent()             {}
func (BlockStart) isEvent()         {}
func (TextDelta) isEvent()          {}
func (AssistantMessage) isEvent()   {}
func (ToolResults) isEvent()        {}
func (*PermissionRequest) isEvent() {}
func (RateLimit) isEvent()          {}
func (Result) isEvent()             {}
func (ControlResult) isEvent()      {}
func (Exited) isEvent()             {}
func (Unknown) isEvent()            {}

type envelope struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Message         json.RawMessage `json:"message"`
	Event           json.RawMessage `json:"event"`
	RequestID       string          `json:"request_id"`
	Request         json.RawMessage `json:"request"`
	Response        json.RawMessage `json:"response"`
	RateLimitInfo   json.RawMessage `json:"rate_limit_info"`

	Model          string   `json:"model"`
	Cwd            string   `json:"cwd"`
	PermissionMode string   `json:"permissionMode"`
	Tools          []string `json:"tools"`
	Status         string   `json:"status"`

	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	Usage        Usage   `json:"usage"`
}

func (c *Client) decode(line []byte) []Event {
	var e envelope
	if err := json.Unmarshal(line, &e); err != nil {
		return []Event{Unknown{Line: string(line)}}
	}
	parent := ""
	if e.ParentToolUseID != nil {
		parent = *e.ParentToolUseID
	}

	switch e.Type {
	case "system":
		switch e.Subtype {
		case "init":
			return []Event{Init{SessionID: e.SessionID, Model: e.Model, Cwd: e.Cwd, PermissionMode: e.PermissionMode, Tools: e.Tools}}
		case "status":
			return []Event{Status{Status: e.Status, PermissionMode: e.PermissionMode}}
		}
		return nil

	case "stream_event":
		if parent != "" {
			return nil // subagent streaming is summarised via its tool calls
		}
		var se struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(e.Event, &se) != nil {
			return nil
		}
		switch se.Type {
		case "content_block_start":
			return []Event{BlockStart{Kind: se.ContentBlock.Type, ToolName: se.ContentBlock.Name}}
		case "content_block_delta":
			if se.Delta.Type == "text_delta" {
				return []Event{TextDelta{Text: se.Delta.Text}}
			}
		}
		return nil

	case "assistant", "user":
		var msg struct {
			Content json.RawMessage `json:"content"`
			Usage   *Usage          `json:"usage"`
		}
		if json.Unmarshal(e.Message, &msg) != nil {
			return nil
		}
		var blocks []ContentBlock
		if json.Unmarshal(msg.Content, &blocks) != nil {
			return nil // plain-string user content is our own echo
		}
		if e.Type == "assistant" {
			return []Event{AssistantMessage{Parent: parent, Blocks: blocks, Usage: msg.Usage}}
		}
		var results []ContentBlock
		for _, b := range blocks {
			if b.Type == "tool_result" {
				results = append(results, b)
			}
		}
		if len(results) == 0 {
			return nil
		}
		return []Event{ToolResults{Parent: parent, Results: results}}

	case "control_request":
		var req struct {
			Subtype     string          `json:"subtype"`
			ToolName    string          `json:"tool_name"`
			DisplayName string          `json:"display_name"`
			Input       json.RawMessage `json:"input"`
			Description string          `json:"description"`
			Suggestions json.RawMessage `json:"permission_suggestions"`
			ToolUseID   string          `json:"tool_use_id"`
		}
		json.Unmarshal(e.Request, &req)
		if req.Subtype != "can_use_tool" {
			c.respondError(e.RequestID, "unsupported control request: "+req.Subtype)
			return nil
		}
		return []Event{&PermissionRequest{
			RequestID:   e.RequestID,
			ToolName:    req.ToolName,
			ToolUseID:   req.ToolUseID,
			Description: req.Description,
			Input:       req.Input,
			Suggestions: req.Suggestions,
		}}

	case "control_response":
		var r struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Error     string          `json:"error"`
			Response  json.RawMessage `json:"response"`
		}
		json.Unmarshal(e.Response, &r)
		tag, _, _ := strings.Cut(r.RequestID, "-")
		if tag == "init" && r.Subtype == "success" {
			var init struct {
				Commands []Command   `json:"commands"`
				Models   []ModelInfo `json:"models"`
				Mode     string      `json:"current_permission_mode"`
			}
			json.Unmarshal(r.Response, &init)
			return []Event{Ready{Commands: init.Commands, Models: init.Models, PermissionMode: init.Mode}}
		}
		return []Event{ControlResult{Tag: tag, Error: r.Error, Body: r.Response}}

	case "rate_limit_event":
		var info struct {
			Status  string `json:"status"`
			Windows struct {
				FiveHour *Window `json:"five_hour"`
				SevenDay *Window `json:"seven_day"`
			} `json:"unifiedWindows"`
		}
		json.Unmarshal(e.RateLimitInfo, &info)
		return []Event{RateLimit{Status: info.Status, FiveHour: info.Windows.FiveHour, SevenDay: info.Windows.SevenDay}}

	case "result":
		return []Event{Result{
			Subtype:    e.Subtype,
			IsError:    e.IsError,
			Text:       e.Result,
			CostUSD:    e.TotalCostUSD,
			DurationMS: e.DurationMS,
			NumTurns:   e.NumTurns,
			Usage:      e.Usage,
		}}
	}
	return nil
}
