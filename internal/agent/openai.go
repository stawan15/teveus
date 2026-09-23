package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// openaiClient speaks the Chat Completions API, which OpenAI, Gemini,
// OpenRouter, Groq, DeepSeek, xAI, Mistral, Ollama and LM Studio all serve.
type openaiClient struct {
	base, key, provider string
}

func (c *openaiClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	return send(ctx, method, strings.TrimRight(c.base, "/")+path, b, func(h http.Header) {
		if c.key != "" {
			h.Set("Authorization", "Bearer "+c.key)
		}
		h.Set("Content-Type", "application/json")
		if c.provider == "openrouter" {
			h.Set("X-Title", "teveus")
		}
	})
}

func (c *openaiClient) Models(ctx context.Context) ([]Model, error) {
	resp, err := c.do(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Context int    `json:"context_length"`
			Created int64  `json:"created"`
			Pricing *struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var ms []Model
	for _, d := range out.Data {
		m := Model{ID: strings.TrimPrefix(d.ID, "models/"), Name: d.Name, Context: d.Context, Created: d.Created}
		if d.Pricing != nil {
			in, err1 := strconv.ParseFloat(d.Pricing.Prompt, 64)
			outp, err2 := strconv.ParseFloat(d.Pricing.Completion, 64)
			if err1 == nil && err2 == nil && in >= 0 && outp >= 0 {
				m.In, m.Out, m.Priced = in*1e6, outp*1e6, true
			}
		}
		ms = append(ms, m)
	}
	// Newest first when the API dates models, else by name.
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].Created != ms[j].Created {
			return ms[i].Created > ms[j].Created
		}
		return ms[i].ID < ms[j].ID
	})
	return ms, nil
}

func (c *openaiClient) Stream(ctx context.Context, req Request, emit func(Chunk)) (*Response, error) {
	msgs := []map[string]any{{"role": "system", "content": req.System}}
	msgs = append(msgs, openaiMessages(req.Messages)...)
	if c.provider == "openrouter" && explicitCache(req.Model) {
		markCache(msgs[0])
		markCache(msgs[len(msgs)-1])
	}
	body := map[string]any{
		"model":          req.Model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		body["tools"] = tools
	}
	if c.provider == "openrouter" {
		body["usage"] = map[string]any{"include": true} // report cost
		// OpenRouter reserves credit for max_tokens, which otherwise defaults
		// to the model's maximum (often 64k) and fails on small balances.
		body["max_tokens"] = 16000
	}
	if lvl := openaiEffort(req.Effort); lvl != "" {
		if c.provider == "openrouter" {
			body["reasoning"] = map[string]any{"effort": lvl}
		} else {
			body["reasoning_effort"] = lvl
		}
	}
	resp, err := c.do(ctx, "POST", "/chat/completions", body)
	if err != nil && req.Effort != "" && rejectsParam(err, "reasoning") {
		// Not a reasoning model: run it without the setting.
		delete(body, "reasoning")
		delete(body, "reasoning_effort")
		resp, err = c.do(ctx, "POST", "/chat/completions", body)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out := &Response{}
	var text strings.Builder
	type call struct {
		tc   ToolCall
		args strings.Builder
	}
	calls := map[int]*call{}
	thinking := false
	var reasoning []map[string]any

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if string(data) == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content          string                       `json:"content"`
					Reasoning        string                       `json:"reasoning"`
					ReasoningContent string                       `json:"reasoning_content"`
					ToolCalls        []map[string]json.RawMessage `json:"tool_calls"`
					ReasoningDetails []map[string]any             `json:"reasoning_details"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int     `json:"prompt_tokens"`
				Completion int     `json:"completion_tokens"`
				Cost       float64 `json:"cost"`
				Details    struct {
					Cached     int `json:"cached_tokens"`
					CacheWrite int `json:"cache_write_tokens"`
				} `json:"prompt_tokens_details"`
				CacheHit int `json:"prompt_cache_hit_tokens"` // DeepSeek
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		if ev.Error != nil {
			return nil, fmt.Errorf("%s: %s", c.provider, ev.Error.Message)
		}
		if ev.Usage != nil {
			cached := max(ev.Usage.Details.Cached, ev.Usage.CacheHit)
			out.Usage.Input = ev.Usage.Prompt - cached - ev.Usage.Details.CacheWrite
			out.Usage.CacheRead = cached
			out.Usage.CacheWrite = ev.Usage.Details.CacheWrite
			out.Usage.Output = ev.Usage.Completion
			out.Usage.Cost = ev.Usage.Cost
		}
		for _, ch := range ev.Choices {
			d := ch.Delta
			if (d.Reasoning != "" || d.ReasoningContent != "") && !thinking {
				thinking = true
				emit(Chunk{Thinking: true})
			}
			if d.Content != "" {
				text.WriteString(d.Content)
				emit(Chunk{Text: d.Content})
			}
			for _, raw := range d.ToolCalls {
				var t struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				}
				b, _ := json.Marshal(raw)
				json.Unmarshal(b, &t)
				cl := calls[t.Index]
				if cl == nil {
					cl = &call{}
					calls[t.Index] = cl
				}
				if t.ID != "" {
					cl.tc.ID = t.ID
				}
				if t.Function.Name != "" && cl.tc.Name == "" {
					cl.tc.Name = t.Function.Name
					emit(Chunk{ToolStart: &ToolCall{ID: cl.tc.ID, Name: cl.tc.Name}})
				}
				cl.args.WriteString(t.Function.Arguments)
				// Keep anything provider-specific (Gemini's thought signature
				// lives in extra_content) to echo it back verbatim.
				for k, v := range raw {
					switch k {
					case "index", "id", "type", "function":
					default:
						if cl.tc.Extra == nil {
							cl.tc.Extra = map[string]json.RawMessage{}
						}
						cl.tc.Extra[k] = v
					}
				}
			}
			for _, rd := range d.ReasoningDetails {
				reasoning = mergeReasoning(reasoning, rd)
			}
			if ch.FinishReason != "" {
				out.Stop = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for n, i := range idx {
		cl := calls[i]
		if cl.tc.ID == "" {
			cl.tc.ID = fmt.Sprintf("call_%d", n) // some providers omit IDs
		}
		args := strings.TrimSpace(cl.args.String())
		if args == "" || !json.Valid([]byte(args)) {
			args = "{}"
		}
		cl.tc.Input = json.RawMessage(args)
		out.ToolCalls = append(out.ToolCalls, cl.tc)
	}
	out.Text = text.String()
	if len(reasoning) > 0 {
		out.Reasoning, _ = json.Marshal(reasoning)
	}
	return out, nil
}

// mergeReasoning folds a streamed reasoning_details fragment into the list:
// fragments with the same index are one detail whose text arrives in pieces.
func mergeReasoning(list []map[string]any, frag map[string]any) []map[string]any {
	idx, hasIdx := frag["index"].(float64)
	for _, d := range list {
		if i, ok := d["index"].(float64); hasIdx && ok && i == idx && d["type"] == frag["type"] {
			for k, v := range frag {
				if s, ok := v.(string); ok && (k == "text" || k == "summary") {
					prev, _ := d[k].(string)
					d[k] = prev + s
				} else if v != nil {
					d[k] = v
				}
			}
			return list
		}
	}
	cp := map[string]any{}
	for k, v := range frag {
		cp[k] = v
	}
	return append(list, cp)
}

// openaiEffort maps teveus's effort levels onto the low/medium/high that
// OpenAI-style APIs take.
func openaiEffort(e string) string {
	switch e {
	case "low", "medium", "high":
		return e
	case "xhigh", "max":
		return "high"
	}
	return ""
}

// routerReasoning returns OpenRouter reasoning_details kept from an earlier
// reply; other providers' reasoning (Anthropic thinking blocks) is dropped.
func routerReasoning(raw json.RawMessage) json.RawMessage {
	var list []map[string]any
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return nil
	}
	for _, d := range list {
		if t, _ := d["type"].(string); !strings.HasPrefix(t, "reasoning.") {
			return nil
		}
	}
	return raw
}

// explicitCache reports whether a model routed through OpenRouter caches
// only with cache_control markers (Anthropic and Gemini). OpenAI, DeepSeek,
// Grok and others cache long prompts on their own.
func explicitCache(model string) bool {
	return strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "google/gemini")
}

// markCache puts a cache breakpoint at the end of a message. Marking the
// system prompt and the newest message caches everything before them, so each
// step of a turn re-reads the conversation from cache.
func markCache(msg map[string]any) {
	switch c := msg["content"].(type) {
	case string:
		if c != "" {
			msg["content"] = []map[string]any{{"type": "text", "text": c, "cache_control": cacheMark}}
		}
	case []map[string]any:
		if len(c) > 0 {
			c[len(c)-1]["cache_control"] = cacheMark
		}
	}
}

func openaiMessages(msgs []Message) []map[string]any {
	var out []map[string]any
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if len(m.Images) == 0 {
				out = append(out, map[string]any{"role": "user", "content": m.Text})
				break
			}
			parts := []map[string]any{{"type": "text", "text": m.Text}}
			for _, img := range m.Images {
				url := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
			out = append(out, map[string]any{"role": "user", "content": parts})
		case "assistant":
			msg := map[string]any{"role": "assistant", "content": m.Text}
			if r := routerReasoning(m.Reasoning); r != nil {
				msg["reasoning_details"] = r
			}
			if len(m.ToolCalls) > 0 {
				var tcs []map[string]any
				for _, tc := range m.ToolCalls {
					call := map[string]any{"id": tc.ID, "type": "function",
						"function": map[string]any{"name": tc.Name, "arguments": string(tc.Input)}}
					for k, v := range tc.Extra {
						call[k] = v
					}
					tcs = append(tcs, call)
				}
				msg["tool_calls"] = tcs
				if m.Text == "" {
					msg["content"] = nil
				}
			}
			out = append(out, msg)
		case "tool":
			content := m.Result
			if m.IsError {
				content = "Error: " + content
			}
			out = append(out, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": content})
		}
	}
	return out
}
