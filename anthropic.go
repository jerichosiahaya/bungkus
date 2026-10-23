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
}

type anthMessage struct {
	Role       string      `json:"role"`
	Content    []anthBlock `json:"content"`
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

func (p *Anthropic) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var out struct {
		Content    []anthBlock `json:"content"`
		StopReason string      `json:"stop_reason"`
		Usage      anthUsage   `json:"usage"`
	}
	if err := p.post(ctx, "/messages", messagesBody(req), &out); err != nil {
		return nil, err
	}
	resp := &ChatResponse{Message: Message{Role: "assistant"}, FinishReason: anthFinishReason(out.StopReason), Usage: out.Usage.usage()}
	var sb strings.Builder
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: string(b.Input)})
		}
	}
	resp.Content = sb.String()
	return resp, nil
}

func (p *Anthropic) ChatStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	body := messagesBody(req)
	body["stream"] = true
	callAt := map[int]int{} // content block index -> position in final.ToolCalls
	var usage anthUsage
	return p.stream(ctx, "/messages", body, func(data []byte, final *ChatResponse) (string, error) {
		var ev struct {
			Type         string    `json:"type"`
			Index        int       `json:"index"`
			ContentBlock anthBlock `json:"content_block"`
			Message      struct {
				Usage anthUsage `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage anthUsage `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return "", err
		}
		switch ev.Type {
		case "message_start":
			usage.merge(ev.Message.Usage)
			final.Usage = usage.usage()
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				callAt[ev.Index] = len(final.ToolCalls)
				final.ToolCalls = append(final.ToolCalls, ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name})
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				return ev.Delta.Text, nil
			case "input_json_delta":
				if i, ok := callAt[ev.Index]; ok {
					final.ToolCalls[i].Arguments += ev.Delta.PartialJSON
				}
			}
		case "content_block_stop":
			if i, ok := callAt[ev.Index]; ok && final.ToolCalls[i].Arguments == "" {
				final.ToolCalls[i].Arguments = "{}" // a tool with no arguments streams no input_json_delta
			}
		case "message_delta":
			final.FinishReason = anthFinishReason(ev.Delta.StopReason)
			usage.merge(ev.Usage)
			final.Usage = usage.usage()
		case "error":
			return "", fmt.Errorf("bungkus: anthropic stream error: %s", ev.Error.Message)
		}
		return "", nil
	})
}

// messagesBody converts a ChatRequest to the Messages API shape: system messages
// move to the top-level system field, tool calls become tool_use blocks, and
// consecutive tool results are grouped into one user message as the API requires.
func messagesBody(req *ChatRequest) map[string]any {
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
				msgs = append(msgs, anthMessage{Role: "user", Content: []anthBlock{b}, toolResult: true})
			}
		default:
			var blocks []anthBlock
			if m.Content != "" {
				blocks = append(blocks, anthBlock{Type: "text", Text: m.Content})
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
	return body
}
