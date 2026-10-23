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
	Content    any          `json:"content"` // nil on assistant messages that only call tools
	ToolCalls  []ccToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

// ccReply is a response message or a stream delta.
type ccReply struct {
	Content   string       `json:"content"`
	ToolCalls []ccToolCall `json:"tool_calls"`
}

func (c *client) chatCompletionsBody(req *ChatRequest) map[string]any {
	msgs := make([]ccMessage, len(req.Messages))
	for i, m := range req.Messages {
		cm := ccMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		if m.Content == "" && len(m.ToolCalls) > 0 {
			cm.Content = nil
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, ccToolCall{ID: tc.ID, Type: "function", Function: ccFunction{tc.Name, tc.Arguments}})
		}
		msgs[i] = cm
	}
	body := map[string]any{"model": req.Model, "messages": msgs}
	if req.MaxTokens > 0 {
		body[c.maxTokensField] = req.MaxTokens
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
	return body
}

// chatCompletions calls an OpenAI-compatible /chat/completions endpoint.
func (c *client) chatCompletions(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var out struct {
		Choices []struct {
			Message ccReply `json:"message"`
		} `json:"choices"`
	}
	if err := c.post(ctx, "/chat/completions", c.chatCompletionsBody(req), &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("bungkus: %s returned no choices", c.name)
	}
	m := out.Choices[0].Message
	resp := &ChatResponse{Message{Role: "assistant", Content: m.Content}}
	for _, tc := range m.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return resp, nil
}

func (c *client) chatCompletionsStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	body := c.chatCompletionsBody(req)
	body["stream"] = true
	var calls []ToolCall
	parse := func(data []byte) (string, error) {
		var ev struct {
			Choices []struct {
				Delta ccReply `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return "", err
		}
		if ev.Error != nil {
			return "", fmt.Errorf("bungkus: %s stream error: %s", c.name, ev.Error.Message)
		}
		if len(ev.Choices) == 0 { // e.g. the final usage chunk
			return "", nil
		}
		d := ev.Choices[0].Delta
		// A tool call arrives in pieces: the first delta has its id and name,
		// later ones append to its arguments. index says which call a piece belongs to.
		for _, tc := range d.ToolCalls {
			i := len(calls)
			if tc.Index != nil {
				i = *tc.Index
			} else if tc.ID == "" && i > 0 {
				i-- // no index: a piece without an id continues the last call
			}
			for len(calls) <= i {
				calls = append(calls, ToolCall{})
			}
			if tc.ID != "" {
				calls[i].ID = tc.ID
			}
			if tc.Function.Name != "" {
				calls[i].Name = tc.Function.Name
			}
			calls[i].Arguments += tc.Function.Arguments
		}
		return d.Content, nil
	}
	done := func() *ChatResponse {
		if len(calls) == 0 {
			return nil
		}
		return &ChatResponse{Message{Role: "assistant", ToolCalls: calls}}
	}
	return c.stream(ctx, "/chat/completions", body, parse, done)
}
