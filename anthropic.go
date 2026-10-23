package bungkus

import (
	"context"
	"net/http"
	"strings"
)

const anthropicURL = "https://api.anthropic.com/v1"

// Anthropic uses the Messages API, not chat completions.
type Anthropic struct {
	BaseURL string // default https://api.anthropic.com/v1
	APIKey  string
	HTTP    *http.Client // nil uses http.DefaultClient
}

func (p *Anthropic) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	var system []string
	var rest []Message
	for _, m := range msgs {
		if m.Role == "system" {
			system = append(system, m.Content)
		} else {
			rest = append(rest, m)
		}
	}
	// ponytail: fixed max_tokens (Anthropic requires it); add an options param when callers need to tune it
	body := map[string]any{"model": model, "messages": rest, "max_tokens": 4096}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	h := map[string]string{"x-api-key": p.APIKey, "anthropic-version": "2023-06-01"}
	if err := post(ctx, p.HTTP, "anthropic", baseOr(p.BaseURL, anthropicURL)+"/messages", h, body, &out); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String(), nil
}
