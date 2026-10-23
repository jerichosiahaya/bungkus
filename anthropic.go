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

func (p *Anthropic) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := p.post(ctx, "/messages", messagesBody(req), &out); err != nil {
		return nil, err
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return &ChatResponse{Content: sb.String()}, nil
}

func (p *Anthropic) ChatStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	body := messagesBody(req)
	body["stream"] = true
	return p.stream(ctx, "/messages", body, func(data []byte) (string, error) {
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"` // only set on text_delta
			} `json:"delta"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return "", err
		}
		switch ev.Type {
		case "content_block_delta":
			return ev.Delta.Text, nil
		case "error":
			return "", fmt.Errorf("bungkus: anthropic stream error: %s", ev.Error.Message)
		}
		return "", nil
	})
}

// messagesBody converts a ChatRequest to the Messages API shape:
// system messages move to the top-level system field, max_tokens is required.
func messagesBody(req *ChatRequest) map[string]any {
	var system []string
	var msgs []Message
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, m.Content)
		} else {
			msgs = append(msgs, m)
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
	return body
}
