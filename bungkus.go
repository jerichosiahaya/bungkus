// Package bungkus is a minimal chat client for OpenAI, Anthropic, Azure OpenAI,
// BytePlus ModelArk and DeepSeek. Everything except Anthropic speaks the
// OpenAI chat-completions format, so there are only two wire formats.
package bungkus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

type Client struct {
	Provider string // openai, anthropic, azure, byteplus, deepseek
	BaseURL  string
	APIKey   string
	HTTP     *http.Client
}

var defaultBaseURL = map[string]string{
	"openai":    "https://api.openai.com/v1",
	"anthropic": "https://api.anthropic.com/v1",
	"byteplus":  "https://ark.ap-southeast.bytepluses.com/api/v3",
	"deepseek":  "https://api.deepseek.com/v1",
	// azure has no default: https://<resource>.openai.azure.com/openai/v1
}

// New returns a client. baseURL may be empty except for azure.
func New(provider, baseURL, apiKey string) (*Client, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL[provider]
	}
	if baseURL == "" {
		return nil, fmt.Errorf("bungkus: unknown provider %q or missing base URL", provider)
	}
	return &Client{Provider: provider, BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: http.DefaultClient}, nil
}

// Chat sends messages and returns the assistant's text reply.
// For azure, model is the deployment name; for byteplus, the endpoint/model ID.
func (c *Client) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	if c.Provider == "anthropic" {
		return c.chatAnthropic(ctx, model, msgs)
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	body := map[string]any{"model": model, "messages": msgs}
	if err := c.post(ctx, "/chat/completions", body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("bungkus: %s returned no choices", c.Provider)
	}
	return out.Choices[0].Message.Content, nil
}

func (c *Client) chatAnthropic(ctx context.Context, model string, msgs []Message) (string, error) {
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
	if err := c.post(ctx, "/messages", body, &out); err != nil {
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

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	switch c.Provider {
	case "anthropic":
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "azure":
		req.Header.Set("api-key", c.APIKey)
	default:
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bungkus: %s %d: %s", c.Provider, resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}
