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
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant: the tools the model wants called
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool: the ToolCall.ID this result answers
}

// Tool describes a function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema of the arguments object; nil means no arguments
}

func (t Tool) schema() json.RawMessage {
	if len(t.Parameters) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t.Parameters
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON object matching the tool's Parameters
}

type ChatRequest struct {
	Model     string // for azure the deployment name, for byteplus the model or endpoint ID
	Messages  []Message
	MaxTokens int // 0 uses the provider default (4096 for anthropic)
	Tools     []Tool

	// Optional sampling settings; nil or empty leaves the provider default.
	// Pointers because 0 is a valid value: Temperature: new(0.2).
	Temperature *float64
	TopP        *float64
	Stop        []string

	// ToolChoice is "" (provider default), "auto", "none", "required"
	// (must call some tool), or a tool name to force that tool.
	ToolChoice string
}

// Finish reasons, normalized across providers.
const (
	FinishStop          = "stop"           // natural end or a stop sequence
	FinishLength        = "length"         // hit MaxTokens
	FinishToolCalls     = "tool_calls"     // the model wants tools run
	FinishContentFilter = "content_filter" // blocked or refused
)

// Usage counts tokens. PromptTokens includes cached input tokens.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.TotalTokens += o.TotalTokens
}

// ChatResponse is the assistant's reply. Append resp.Message to the history
// to continue the conversation, e.g. after running its ToolCalls.
type ChatResponse struct {
	Message
	FinishReason string // one of the Finish constants, or the provider's own value if unknown
	Usage        Usage
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	// ChatStream yields text deltas as they arrive. The last chunk has no text;
	// it carries the complete ToolCalls, FinishReason and Usage. Stop ranging to cancel.
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

// stream reads a server-sent events response. parse handles each data payload:
// it returns any text delta to yield now, and records tool calls, finish reason
// and usage on final, which is yielded as the last chunk.
func (c *client) stream(ctx context.Context, path string, body any, parse func(data []byte, final *ChatResponse) (string, error)) iter.Seq2[*ChatResponse, error] {
	return func(yield func(*ChatResponse, error) bool) {
		resp, err := c.do(ctx, path, body)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()
		final := &ChatResponse{Message: Message{Role: "assistant"}}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
			if !ok {
				continue
			}
			data = bytes.TrimSpace(data)
			if string(data) == "[DONE]" {
				break
			}
			text, err := parse(data, final)
			if err != nil {
				yield(nil, err)
				return
			}
			if text != "" && !yield(&ChatResponse{Message: Message{Role: "assistant", Content: text}}, nil) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			yield(nil, err)
			return
		}
		yield(final, nil)
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
