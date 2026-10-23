package bungkus

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
)

// The OpenAI chat-completions wire format, shared by OpenAI, Azure, BytePlus and DeepSeek.

type ccFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type ccToolCall struct {
	Index    *int       `json:"index,omitempty"` // stream deltas only
	ID       string     `json:"id,omitempty"`
	Type     string     `json:"type,omitempty"`
	Function ccFunction `json:"function"`
}

type ccMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content"` // string, content parts, or nil on assistant messages that only call tools
	ToolCalls  []ccToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	// DeepSeek requires reasoning_content back on assistant messages once
	// tools are in play; providers that never return it never get it back.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// ccReply is a response message or a stream delta.
type ccReply struct {
	Content          string       `json:"content"`
	ReasoningContent string       `json:"reasoning_content"`
	ToolCalls        []ccToolCall `json:"tool_calls"`
}

func ccContent(m Message) (any, error) {
	if len(m.Parts) == 0 {
		if m.Content == "" && len(m.ToolCalls) > 0 {
			return nil, nil
		}
		return m.Content, nil
	}
	var parts []map[string]any
	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	for _, p := range m.Parts {
		switch p.Type {
		case PartImage:
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": p.URL}})
		case PartFile:
			parts = append(parts, map[string]any{"type": "file", "file": map[string]any{"file_data": p.URL, "filename": p.Name}})
		default:
			return nil, fmt.Errorf("bungkus: unknown part type %q", p.Type)
		}
	}
	return parts, nil
}

func (c *client) chatCompletionsBody(req *ChatRequest) (map[string]any, error) {
	msgs := make([]ccMessage, len(req.Messages))
	for i, m := range req.Messages {
		content, err := ccContent(m)
		if err != nil {
			return nil, err
		}
		cm := ccMessage{Role: m.Role, Content: content, ToolCallID: m.ToolCallID, ReasoningContent: m.Reasoning}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, ccToolCall{ID: tc.ID, Type: "function", Function: ccFunction{tc.Name, tc.Arguments}})
		}
		msgs[i] = cm
	}
	body := map[string]any{"model": req.Model, "messages": msgs}
	if req.MaxTokens > 0 {
		body[c.maxTokensField] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = req.ReasoningEffort
	}
	if f := req.ResponseFormat; f != nil {
		if f.Schema == nil {
			body["response_format"] = map[string]any{"type": "json_object"}
		} else {
			name := f.Name
			if name == "" {
				name = "response"
			}
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{
				"name": name, "schema": f.Schema, "strict": true,
			}}
		}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.schema(),
			}}
		}
		body["tools"] = tools
	}
	switch req.ToolChoice {
	case "":
	case "auto", "none", "required":
		body["tool_choice"] = req.ToolChoice
	default:
		body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": req.ToolChoice}}
	}
	return body, nil
}

// chatCompletions calls an OpenAI-compatible /chat/completions endpoint.
func (c *client) chatCompletions(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return c.chat(ctx, req, func(ctx context.Context) (*ChatResponse, error) {
		body, err := c.chatCompletionsBody(req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Choices []struct {
				Message      ccReply `json:"message"`
				FinishReason string  `json:"finish_reason"`
			} `json:"choices"`
			Usage Usage `json:"usage"`
		}
		if err := c.post(ctx, "/chat/completions", body, &out); err != nil {
			return nil, err
		}
		if len(out.Choices) == 0 {
			return nil, fmt.Errorf("bungkus: %s returned no choices", c.name)
		}
		ch := out.Choices[0]
		resp := &ChatResponse{
			Message:      Message{Role: "assistant", Content: ch.Message.Content, Reasoning: ch.Message.ReasoningContent},
			FinishReason: ch.FinishReason,
			Usage:        out.Usage,
		}
		for _, tc := range ch.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
		return resp, nil
	})
}

func (c *client) chatCompletionsStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	return c.chatStream(ctx, req, func(ctx context.Context) iter.Seq2[*ChatResponse, error] {
		body, err := c.chatCompletionsBody(req)
		if err != nil {
			return func(yield func(*ChatResponse, error) bool) { yield(nil, err) }
		}
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
		return c.stream(ctx, "/chat/completions", body, func(data []byte, final *ChatResponse) (Message, error) {
			var ev struct {
				Choices []struct {
					Delta        ccReply `json:"delta"`
					FinishReason string  `json:"finish_reason"`
				} `json:"choices"`
				Usage *Usage `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(data, &ev); err != nil {
				return Message{}, err
			}
			if ev.Error != nil {
				return Message{}, fmt.Errorf("bungkus: %s stream error: %s", c.name, ev.Error.Message)
			}
			if ev.Usage != nil {
				final.Usage = *ev.Usage
			}
			if len(ev.Choices) == 0 { // the usage-only chunk at the end
				return Message{}, nil
			}
			ch := ev.Choices[0]
			if ch.FinishReason != "" {
				final.FinishReason = ch.FinishReason
			}
			// A tool call arrives in pieces: the first delta has its id and name,
			// later ones append to its arguments. index says which call a piece belongs to.
			calls := &final.ToolCalls
			for _, tc := range ch.Delta.ToolCalls {
				i := len(*calls)
				if tc.Index != nil {
					i = *tc.Index
				} else if tc.ID == "" && i > 0 {
					i-- // no index: a piece without an id continues the last call
				}
				for len(*calls) <= i {
					*calls = append(*calls, ToolCall{})
				}
				if tc.ID != "" {
					(*calls)[i].ID = tc.ID
				}
				if tc.Function.Name != "" {
					(*calls)[i].Name = tc.Function.Name
				}
				(*calls)[i].Arguments += tc.Function.Arguments
			}
			return Message{Content: ch.Delta.Content, Reasoning: ch.Delta.ReasoningContent}, nil
		})
	})
}
