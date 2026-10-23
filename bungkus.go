// Package bungkus is a minimal chat client for OpenAI, Anthropic, Azure OpenAI,
// BytePlus ModelArk and DeepSeek. Each provider lives in its own file.
package bungkus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	Parts      []Part     `json:"parts,omitempty"`        // images and files sent with Content
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant: the tools the model wants called
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool: the ToolCall.ID this result answers

	// Reasoning is the model's readable thinking, when the provider returns it
	// (DeepSeek and BytePlus reasoning_content, Claude thinking summaries).
	Reasoning string `json:"reasoning,omitempty"`
	// ReasoningData is provider state that must be sent back unchanged with the
	// message, such as Claude's signed thinking blocks. Keep it in your history.
	ReasoningData json.RawMessage `json:"reasoning_data,omitempty"`
}

// Part types.
const (
	PartImage = "image"
	PartFile  = "file" // e.g. a PDF
)

// Part is an image or file attached to a message.
type Part struct {
	Type string `json:"type"`           // PartImage or PartFile
	URL  string `json:"url"`            // https URL or data URL (see DataURL); chat-completions providers need data URLs for files
	Name string `json:"name,omitempty"` // file name
}

// DataURL encodes data as a data URL for Part.URL.
func DataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func parseDataURL(u string) (mimeType, data string, ok bool) {
	rest, ok := strings.CutPrefix(u, "data:")
	if !ok {
		return "", "", false
	}
	meta, data, ok := strings.Cut(rest, ",")
	mimeType, isBase64 := strings.CutSuffix(meta, ";base64")
	return mimeType, data, ok && isBase64
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

// ResponseFormat asks for a JSON reply.
type ResponseFormat struct {
	Name string // schema name, required by OpenAI; "" means "response"
	// Schema is the JSON Schema the reply must match. OpenAI and Anthropic
	// require "additionalProperties": false on every object. nil asks for any
	// JSON object, which Anthropic does not support and DeepSeek requires.
	Schema json.RawMessage
}

type ChatRequest struct {
	Model     string // for azure the deployment name, for byteplus the model or endpoint ID
	Messages  []Message
	MaxTokens int // 0 uses the provider default (4096 for anthropic)
	Tools     []Tool

	// Optional sampling settings; nil or empty leaves the provider default.
	// Pointers because 0 is a valid value; on Go 1.26+ write Temperature: new(0.2).
	Temperature *float64
	TopP        *float64
	Stop        []string

	// ToolChoice is "" (provider default), "auto", "none", "required"
	// (must call some tool), or a tool name to force that tool.
	ToolChoice string

	// ReasoningEffort is passed through as reasoning_effort (OpenAI, Azure,
	// DeepSeek, BytePlus) or output_config.effort (Anthropic). Valid values
	// depend on the model, e.g. "low", "medium", "high", "max".
	ReasoningEffort string

	ResponseFormat *ResponseFormat // nil means plain text
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
	// ChatStream yields Content and Reasoning deltas as they arrive. The last
	// chunk carries the complete ToolCalls, ReasoningData, FinishReason and
	// Usage. Collect joins the chunks into one response. Stop ranging to cancel.
	ChatStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error]
}

// Collect reads a stream to the end and joins its chunks into one response.
// On error it returns what arrived so far along with the error.
func Collect(stream iter.Seq2[*ChatResponse, error]) (*ChatResponse, error) {
	var c collector
	for chunk, err := range stream {
		if err != nil {
			return c.result(), err
		}
		c.add(chunk)
	}
	return c.result(), nil
}

type collector struct {
	resp               ChatResponse
	content, reasoning strings.Builder
}

func (c *collector) add(chunk *ChatResponse) {
	c.content.WriteString(chunk.Content)
	c.reasoning.WriteString(chunk.Reasoning)
	c.resp.ToolCalls = append(c.resp.ToolCalls, chunk.ToolCalls...)
	if chunk.ReasoningData != nil {
		c.resp.ReasoningData = chunk.ReasoningData
	}
	if chunk.FinishReason != "" {
		c.resp.FinishReason = chunk.FinishReason
	}
	if chunk.Usage != (Usage{}) {
		c.resp.Usage = chunk.Usage
	}
}

func (c *collector) result() *ChatResponse {
	r := c.resp
	r.Role, r.Content, r.Reasoning = "assistant", c.content.String(), c.reasoning.String()
	return &r
}

// APIError is returned when a provider responds with a non-2xx status.
// Use errors.As to inspect StatusCode.
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

// WithRetries sets how many times a request is retried after a 408, 429 or
// 5xx response or a network error. The default is 2; 0 disables retries.
// Waits follow the Retry-After header when present, else exponential backoff.
func WithRetries(n int) Option {
	return func(c *client) { c.retries = n }
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
	retries        int
}

func newClient(name, baseURL string, headers map[string]string, opts []Option) client {
	c := client{name: name, baseURL: baseURL, headers: headers, maxTokensField: "max_tokens", hc: http.DefaultClient, retries: 2}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// chat wraps a provider's Chat with callbacks.
func (c *client) chat(ctx context.Context, req *ChatRequest, call func(context.Context) (*ChatResponse, error)) (*ChatResponse, error) {
	ctx, end := StartRun(ctx, RunInfo{Component: ComponentModel, Name: c.name}, req)
	resp, err := call(ctx)
	end(resp, err)
	return resp, err
}

// chatStream wraps a provider's ChatStream with callbacks, which see the
// collected response when the stream ends or the caller stops early.
func (c *client) chatStream(ctx context.Context, req *ChatRequest, stream func(context.Context) iter.Seq2[*ChatResponse, error]) iter.Seq2[*ChatResponse, error] {
	return func(yield func(*ChatResponse, error) bool) {
		ctx, end := StartRun(ctx, RunInfo{Component: ComponentModel, Name: c.name}, req)
		var col collector
		for chunk, err := range stream(ctx) {
			if err != nil {
				end(nil, err)
				yield(nil, err)
				return
			}
			col.add(chunk)
			if !yield(chunk, nil) {
				break
			}
		}
		end(col.result(), nil)
	}
}

// stream reads a server-sent events response. parse handles each data payload:
// it returns any Content or Reasoning delta to yield now, and records tool
// calls, reasoning data, finish reason and usage on final, the last chunk.
func (c *client) stream(ctx context.Context, path string, body any, parse func(data []byte, final *ChatResponse) (Message, error)) iter.Seq2[*ChatResponse, error] {
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
			delta, err := parse(data, final)
			if err != nil {
				yield(nil, err)
				return
			}
			if delta.Content != "" || delta.Reasoning != "" {
				delta.Role = "assistant"
				if !yield(&ChatResponse{Message: delta}, nil) {
					return
				}
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

// do sends a JSON POST, retrying transient failures, and returns the response
// or an *APIError on a non-2xx status.
func (c *client) do(ctx context.Context, path string, body any) (*http.Response, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("bungkus: %s has no base URL", c.name)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		resp, err := c.hc.Do(req)
		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil || attempt >= c.retries {
				return nil, err
			}
		case resp.StatusCode < 300:
			return resp, nil
		case retryable(resp.StatusCode) && attempt < c.retries:
			wait = retryAfter(resp.Header)
			resp.Body.Close()
		default:
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			return nil, &APIError{Provider: c.name, StatusCode: resp.StatusCode, Body: string(raw)}
		}
		if wait == 0 {
			wait = backoff(attempt)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// retryAfter reads OpenAI's retry-after-ms or the standard Retry-After header.
func retryAfter(h http.Header) time.Duration {
	if ms, err := strconv.Atoi(h.Get("retry-after-ms")); err == nil {
		return time.Duration(ms) * time.Millisecond
	}
	v := h.Get("Retry-After")
	if s, err := strconv.Atoi(v); err == nil {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

// backoff is a variable so tests can skip waiting.
var backoff = defaultBackoff

// defaultBackoff is 0.5s, 1s, 2s, ... capped at 8s, with ±25% jitter.
func defaultBackoff(attempt int) time.Duration {
	d := min(500*time.Millisecond<<attempt, 8*time.Second)
	return d*3/4 + rand.N(d/2)
}
