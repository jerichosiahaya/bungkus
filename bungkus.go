// Package bungkus is a minimal chat client for OpenAI, Anthropic, Azure OpenAI,
// BytePlus ModelArk and DeepSeek. Each provider lives in its own file.
package bungkus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
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

// ChatResponse is a full reply from Chat, or one text delta from ChatStream.
type ChatResponse struct {
	Content string
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	// ChatStream yields text deltas as they arrive. Stop ranging to cancel.
	ChatStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error]
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

func (c *client) chatCompletionsBody(req *ChatRequest) map[string]any {
	body := map[string]any{"model": req.Model, "messages": req.Messages}
	if req.MaxTokens > 0 {
		body[c.maxTokensField] = req.MaxTokens
	}
	return body
}

// chatCompletions calls an OpenAI-compatible /chat/completions endpoint.
func (c *client) chatCompletions(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := c.post(ctx, "/chat/completions", c.chatCompletionsBody(req), &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("bungkus: %s returned no choices", c.name)
	}
	return &ChatResponse{Content: out.Choices[0].Message.Content}, nil
}

func (c *client) chatCompletionsStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	body := c.chatCompletionsBody(req)
	body["stream"] = true
	return c.stream(ctx, "/chat/completions", body, func(data []byte) (string, error) {
		var ev struct {
			Choices []struct {
				Delta Message `json:"delta"`
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
		return ev.Choices[0].Delta.Content, nil
	})
}

// stream reads a server-sent events response, passing each data payload to parse
// and yielding the non-empty text it returns.
func (c *client) stream(ctx context.Context, path string, body any, parse func(data []byte) (string, error)) iter.Seq2[*ChatResponse, error] {
	return func(yield func(*ChatResponse, error) bool) {
		resp, err := c.do(ctx, path, body)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
			if !ok {
				continue
			}
			data = bytes.TrimSpace(data)
			if string(data) == "[DONE]" {
				return
			}
			text, err := parse(data)
			if err != nil {
				yield(nil, err)
				return
			}
			if text != "" && !yield(&ChatResponse{Content: text}, nil) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			yield(nil, err)
		}
	}
}

func (c *client) post(ctx context.Context, path string, body, out any) error {
	resp, err := c.do(ctx, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// do sends a JSON POST and returns the response, or an *APIError on a non-2xx status.
func (c *client) do(ctx context.Context, path string, body any) (*http.Response, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("bungkus: %s has no base URL", c.name)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return nil, &APIError{Provider: c.name, StatusCode: resp.StatusCode, Body: string(raw)}
	}
	return resp, nil
}
