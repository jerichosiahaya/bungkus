package bungkus

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
)

var _ Provider = (*Anthropic)(nil)

// Anthropic uses the Messages API, not chat completions.
type Anthropic struct{ client }

// NewAnthropic returns an Anthropic client using https://api.anthropic.com/v1.
func NewAnthropic(apiKey string, opts ...Option) *Anthropic {
	return &Anthropic{newClient("anthropic", "https://api.anthropic.com/v1", map[string]string{"x-api-key": apiKey, "anthropic-version": "2023-06-01"}, opts)}
}

// anthBlock is a content block in a request or response.
type anthBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   string          `json:"content,omitempty"`     // tool_result
	Thinking  string          `json:"thinking,omitempty"`    // thinking (responses only; sent back raw)
}

type anthMessage struct {
	Role       string `json:"role"`
	Content    []any  `json:"content"` // anthBlock, a part's map, or a raw thinking block
	toolResult bool
}

type anthUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// merge copies the non-zero counts from o; stream events report usage piecemeal.
func (u *anthUsage) merge(o anthUsage) {
	for _, f := range []struct {
		dst *int
		src int
	}{
		{&u.InputTokens, o.InputTokens}, {&u.OutputTokens, o.OutputTokens},
		{&u.CacheCreationInputTokens, o.CacheCreationInputTokens}, {&u.CacheReadInputTokens, o.CacheReadInputTokens},
	} {
		if f.src != 0 {
			*f.dst = f.src
		}
	}
}

func (u anthUsage) usage() Usage {
	in := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	return Usage{PromptTokens: in, CompletionTokens: u.OutputTokens, TotalTokens: in + u.OutputTokens}
}

func anthFinishReason(stop string) string {
	switch stop {
	case "end_turn", "stop_sequence":
		return FinishStop
	case "max_tokens":
		return FinishLength
	case "tool_use":
		return FinishToolCalls
	case "refusal":
		return FinishContentFilter
	}
	return stop
}

// Thinking blocks (thinking, redacted_thinking) must go back to the API
// exactly as received during tool use, so they are kept raw in ReasoningData.
func isThinking(blockType string) bool {
	return blockType == "thinking" || blockType == "redacted_thinking"
}

func (p *Anthropic) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.chat(ctx, req, func(ctx context.Context) (*ChatResponse, error) {
		body, err := messagesBody(req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Content    []json.RawMessage `json:"content"`
			StopReason string            `json:"stop_reason"`
			Usage      anthUsage         `json:"usage"`
		}
		if err := p.post(ctx, "/messages", body, &out); err != nil {
			return nil, err
		}
		resp := &ChatResponse{Message: Message{Role: "assistant"}, FinishReason: anthFinishReason(out.StopReason), Usage: out.Usage.usage()}
		var text, reasoning strings.Builder
		var thinking []json.RawMessage
		for _, raw := range out.Content {
			var b anthBlock
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, err
			}
			switch {
			case b.Type == "text":
				text.WriteString(b.Text)
			case b.Type == "tool_use":
				resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: string(b.Input)})
			case isThinking(b.Type):
				thinking = append(thinking, raw)
				reasoning.WriteString(b.Thinking)
			}
		}
		resp.Content, resp.Reasoning = text.String(), reasoning.String()
		if len(thinking) > 0 {
			resp.ReasoningData, _ = json.Marshal(thinking)
		}
		return resp, nil
	})
}

func (p *Anthropic) ChatStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	return p.chatStream(ctx, req, func(ctx context.Context) iter.Seq2[*ChatResponse, error] {
		body, err := messagesBody(req)
		if err != nil {
			return func(yield func(*ChatResponse, error) bool) { yield(nil, err) }
		}
		body["stream"] = true
		callAt := map[int]int{} // content block index -> position in final.ToolCalls
		var usage anthUsage
		var thinking []json.RawMessage
		var think struct { // the thinking block being streamed
			index           int
			text, signature strings.Builder
		}
		think.index = -1
		return p.stream(ctx, "/messages", body, func(data []byte, final *ChatResponse) (Message, error) {
			var ev struct {
				Type         string          `json:"type"`
				Index        int             `json:"index"`
				ContentBlock json.RawMessage `json:"content_block"`
				Message      struct {
					Usage anthUsage `json:"usage"`
				} `json:"message"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
					StopReason  string `json:"stop_reason"`
				} `json:"delta"`
				Usage anthUsage `json:"usage"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(data, &ev); err != nil {
				return Message{}, err
			}
			switch ev.Type {
			case "message_start":
				usage.merge(ev.Message.Usage)
				final.Usage = usage.usage()
			case "content_block_start":
				var b anthBlock
				json.Unmarshal(ev.ContentBlock, &b) // the event itself parsed, so this cannot fail
				switch b.Type {
				case "tool_use":
					callAt[ev.Index] = len(final.ToolCalls)
					final.ToolCalls = append(final.ToolCalls, ToolCall{ID: b.ID, Name: b.Name})
				case "thinking":
					think.index = ev.Index
					think.text.Reset()
					think.signature.Reset()
				case "redacted_thinking": // arrives whole, no deltas
					thinking = append(thinking, ev.ContentBlock)
					final.ReasoningData, _ = json.Marshal(thinking)
				}
			case "content_block_delta":
				switch ev.Delta.Type {
				case "text_delta":
					return Message{Content: ev.Delta.Text}, nil
				case "thinking_delta":
					think.text.WriteString(ev.Delta.Thinking)
					return Message{Reasoning: ev.Delta.Thinking}, nil
				case "signature_delta":
					think.signature.WriteString(ev.Delta.Signature)
				case "input_json_delta":
					if i, ok := callAt[ev.Index]; ok {
						final.ToolCalls[i].Arguments += ev.Delta.PartialJSON
					}
				}
			case "content_block_stop":
				if i, ok := callAt[ev.Index]; ok && final.ToolCalls[i].Arguments == "" {
					final.ToolCalls[i].Arguments = "{}" // a tool with no arguments streams no input_json_delta
				}
				if ev.Index == think.index {
					block, _ := json.Marshal(map[string]string{"type": "thinking", "thinking": think.text.String(), "signature": think.signature.String()})
					thinking = append(thinking, block)
					final.ReasoningData, _ = json.Marshal(thinking)
					think.index = -1
				}
			case "message_delta":
				final.FinishReason = anthFinishReason(ev.Delta.StopReason)
				usage.merge(ev.Usage)
				final.Usage = usage.usage()
			case "error":
				return Message{}, fmt.Errorf("bungkus: anthropic stream error: %s", ev.Error.Message)
			}
			return Message{}, nil
		})
	})
}

func anthPart(p Part) (map[string]any, error) {
	var blockType string
	switch p.Type {
	case PartImage:
		blockType = "image"
	case PartFile:
		blockType = "document"
	default:
		return nil, fmt.Errorf("bungkus: unknown part type %q", p.Type)
	}
	source := map[string]any{"type": "url", "url": p.URL}
	if mimeType, data, ok := parseDataURL(p.URL); ok {
		source = map[string]any{"type": "base64", "media_type": mimeType, "data": data}
	}
	block := map[string]any{"type": blockType, "source": source}
	if blockType == "document" && p.Name != "" {
		block["title"] = p.Name
	}
	return block, nil
}

// messagesBody converts a ChatRequest to the Messages API shape: system messages
// move to the top-level system field, thinking blocks are restored from
// ReasoningData, tool calls become tool_use blocks, and consecutive tool
// results are grouped into one user message as the API requires.
func messagesBody(req *ChatRequest) (map[string]any, error) {
	var system []string
	var msgs []anthMessage
	for _, m := range req.Messages {
		switch {
		case m.Role == "system":
			system = append(system, m.Content)
		case m.Role == "tool":
			b := anthBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if n := len(msgs); n > 0 && msgs[n-1].toolResult {
				msgs[n-1].Content = append(msgs[n-1].Content, b)
			} else {
				msgs = append(msgs, anthMessage{Role: "user", Content: []any{b}, toolResult: true})
			}
		default:
			var blocks []any
			if len(m.ReasoningData) > 0 {
				var thinking []json.RawMessage
				if err := json.Unmarshal(m.ReasoningData, &thinking); err != nil {
					return nil, fmt.Errorf("bungkus: anthropic ReasoningData: %w", err)
				}
				for _, t := range thinking {
					blocks = append(blocks, t)
				}
			}
			if m.Content != "" {
				blocks = append(blocks, anthBlock{Type: "text", Text: m.Content})
			}
			for _, p := range m.Parts {
				block, err := anthPart(p)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, block)
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			msgs = append(msgs, anthMessage{Role: m.Role, Content: blocks})
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	body := map[string]any{"model": req.Model, "messages": msgs, "max_tokens": maxTokens}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}
	outputConfig := map[string]any{}
	if req.ReasoningEffort != "" {
		outputConfig["effort"] = req.ReasoningEffort
	}
	if f := req.ResponseFormat; f != nil {
		if f.Schema == nil {
			return nil, fmt.Errorf("bungkus: anthropic needs ResponseFormat.Schema; it has no schemaless JSON mode")
		}
		outputConfig["format"] = map[string]any{"type": "json_schema", "schema": f.Schema}
	}
	if len(outputConfig) > 0 {
		body["output_config"] = outputConfig
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{"name": t.Name, "input_schema": t.schema()}
			if t.Description != "" {
				tools[i]["description"] = t.Description
			}
		}
		body["tools"] = tools
	}
	switch req.ToolChoice {
	case "":
	case "auto", "none":
		body["tool_choice"] = map[string]any{"type": req.ToolChoice}
	case "required":
		body["tool_choice"] = map[string]any{"type": "any"}
	default:
		body["tool_choice"] = map[string]any{"type": "tool", "name": req.ToolChoice}
	}
	return body, nil
}
