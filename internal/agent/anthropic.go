package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type anthropicClient struct {
	base, key string
}

var cacheMark = map[string]string{"type": "ephemeral"}

func (c *anthropicClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.base, "/")+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := httpDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, apiError(resp)
	}
	return resp, nil
}

func (c *anthropicClient) Models(ctx context.Context) ([]Model, error) {
	resp, err := c.do(ctx, "GET", "/models?limit=1000", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var ms []Model
	for _, d := range out.Data {
		ms = append(ms, Model{ID: d.ID, Name: d.DisplayName})
	}
	return ms, nil
}

func (c *anthropicClient) Stream(ctx context.Context, req Request, emit func(Chunk)) (*Response, error) {
	var tools []map[string]any
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = cacheMark
	}
	msgs := anthropicMessages(req.Messages)
	// Rolling cache breakpoint on the newest message: each turn re-reads the
	// whole conversation from cache instead of paying full price for it.
	if n := len(msgs); n > 0 {
		if blocks, _ := msgs[n-1]["content"].([]map[string]any); len(blocks) > 0 {
			blocks[len(blocks)-1]["cache_control"] = cacheMark
		}
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": 16000,
		"stream":     true,
		"system":     []map[string]any{{"type": "text", "text": req.System, "cache_control": cacheMark}},
		"messages":   msgs,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	resp, err := c.do(ctx, "POST", "/messages", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out := &Response{}
	type block struct {
		kind string
		call *ToolCall
		json strings.Builder
	}
	blocks := map[int]*block{}
	var text strings.Builder

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage anthropicUsage `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(bytes.TrimSpace(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			u := ev.Message.Usage
			out.Usage.Input, out.Usage.CacheRead, out.Usage.CacheWrite = u.Input, u.CacheRead, u.CacheWrite
		case "content_block_start":
			b := &block{kind: ev.ContentBlock.Type}
			blocks[ev.Index] = b
			switch b.kind {
			case "tool_use":
				b.call = &ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
				emit(Chunk{ToolStart: b.call})
			case "thinking", "redacted_thinking":
				emit(Chunk{Thinking: true})
			}
		case "content_block_delta":
			b := blocks[ev.Index]
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				emit(Chunk{Text: ev.Delta.Text})
			case "input_json_delta":
				if b != nil {
					b.json.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			if b := blocks[ev.Index]; b != nil && b.call != nil {
				in := strings.TrimSpace(b.json.String())
				if in == "" {
					in = "{}"
				}
				b.call.Input = json.RawMessage(in)
				out.ToolCalls = append(out.ToolCalls, *b.call)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				out.Stop = ev.Delta.StopReason
			}
			if ev.Usage.Output > 0 {
				out.Usage.Output = ev.Usage.Output
			}
		case "error":
			return nil, fmt.Errorf("anthropic: %s", ev.Error.Message)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out.Text = text.String()
	return out, nil
}

type anthropicUsage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

// anthropicMessages converts the neutral history, grouping tool results into
// a user turn and merging adjacent same-role turns as the API requires.
func anthropicMessages(msgs []Message) []map[string]any {
	var out []map[string]any
	push := func(role string, blocks ...map[string]any) {
		if n := len(out); n > 0 && out[n-1]["role"] == role {
			out[n-1]["content"] = append(out[n-1]["content"].([]map[string]any), blocks...)
			return
		}
		out = append(out, map[string]any{"role": role, "content": blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			push("user", map[string]any{"type": "text", "text": m.Text})
		case "assistant":
			var blocks []map[string]any
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
			}
			for _, tc := range m.ToolCalls {
				var in any = map[string]any{}
				json.Unmarshal(tc.Input, &in)
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": in})
			}
			if len(blocks) > 0 {
				push("assistant", blocks...)
			}
		case "tool":
			push("user", map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Result, "is_error": m.IsError})
		}
	}
	return out
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4000))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(b))
	var list []json.RawMessage // Google wraps errors in a list
	if json.Unmarshal(b, &list) == nil && len(list) > 0 {
		b = list[0]
	}
	if json.Unmarshal(b, &e) == nil {
		if e.Error.Message != "" {
			msg = e.Error.Message
		} else if e.Message != "" {
			msg = e.Message
		}
	}
	switch resp.StatusCode {
	case 401, 403:
		return fmt.Errorf("authentication failed (%d): %s — check your key with /login", resp.StatusCode, msg)
	case 402:
		return fmt.Errorf("out of credits: %s\nAdd credits with the provider, or pick a free model: /model, then type \"free\"", msg)
	case 429:
		return fmt.Errorf("rate limited (429): %s", msg)
	}
	return fmt.Errorf("%s: %s", resp.Status, msg)
}
