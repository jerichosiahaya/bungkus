// Package bungkus is a minimal chat client for OpenAI, Anthropic, Azure OpenAI,
// BytePlus ModelArk and DeepSeek. Each provider lives in its own file.
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

type ChatRequest struct {
	Model     string // for azure the deployment name, for byteplus the model or endpoint ID
	Messages  []Message
	MaxTokens int // 0 uses the provider default (4096 for anthropic)
}

type ChatResponse struct {
	Content string
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}

// APIError is returned when a provider responds with a non-2xx status.
// Use errors.As to inspect StatusCode, e.g. to retry on 429.
type APIError struct {
	Provider   string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bungkus: %s returned %d: %s", e.Provider, e.StatusCode, e.Body)
}

type Option func(*client)

// WithBaseURL overrides the provider's default API base URL.
func WithBaseURL(url string) Option {
	return func(c *client) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithHTTPClient sets the HTTP client, e.g. for timeouts or proxies.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *client) { c.hc = hc }
}

// New returns a provider by name: openai, anthropic, azure, byteplus, deepseek.
// Useful when the provider comes from config; otherwise call NewOpenAI etc. directly.
func New(name, apiKey string, opts ...Option) (Provider, error) {
	switch name {
	case "openai":
		return NewOpenAI(apiKey, opts...), nil
	case "anthropic":
		return NewAnthropic(apiKey, opts...), nil
	case "azure":
		p := NewAzure("", apiKey, opts...)
		if p.baseURL == "" {
			return nil, fmt.Errorf("bungkus: azure needs WithBaseURL(\"https://<resource>.openai.azure.com/openai/v1\")")
		}
		return p, nil
	case "byteplus":
		return NewBytePlus(apiKey, opts...), nil
	case "deepseek":
		return NewDeepSeek(apiKey, opts...), nil
	}
	return nil, fmt.Errorf("bungkus: unknown provider %q", name)
}

// client holds what every provider shares: endpoint, auth and HTTP plumbing.
type client struct {
	name           string
	baseURL        string
	headers        map[string]string // auth headers
	maxTokensField string            // chat completions only: "max_tokens" or "max_completion_tokens"
	hc             *http.Client
}

func newClient(name, baseURL string, headers map[string]string, opts []Option) client {
	c := client{name: name, baseURL: baseURL, headers: headers, maxTokensField: "max_tokens", hc: http.DefaultClient}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// chatCompletions calls an OpenAI-compatible /chat/completions endpoint.
func (c *client) chatCompletions(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body := map[string]any{"model": req.Model, "messages": req.Messages}
	if req.MaxTokens > 0 {
		body[c.maxTokensField] = req.MaxTokens
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := c.post(ctx, "/chat/completions", body, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("bungkus: %s returned no choices", c.name)
	}
	return &ChatResponse{Content: out.Choices[0].Message.Content}, nil
}

func (c *client) post(ctx context.Context, path string, body, out any) error {
	if c.baseURL == "" {
		return fmt.Errorf("bungkus: %s has no base URL", c.name)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &APIError{Provider: c.name, StatusCode: resp.StatusCode, Body: string(raw)}
	}
	return json.Unmarshal(raw, out)
}
