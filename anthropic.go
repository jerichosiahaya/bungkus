package bungkus

import (
	"context"
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
		maxTokens = 4096 // required by the Messages API
	}
	body := map[string]any{"model": req.Model, "messages": msgs, "max_tokens": maxTokens}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := p.post(ctx, "/messages", body, &out); err != nil {
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
