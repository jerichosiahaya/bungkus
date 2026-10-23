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

type Provider interface {
	// Chat sends messages and returns the assistant's text reply.
	Chat(ctx context.Context, model string, msgs []Message) (string, error)
}

// New returns a provider by name: openai, anthropic, azure, byteplus, deepseek.
// baseURL may be empty to use the provider's default, except for azure.
func New(provider, baseURL, apiKey string) (Provider, error) {
	switch provider {
	case "openai":
		return &OpenAI{BaseURL: baseURL, APIKey: apiKey}, nil
	case "anthropic":
		return &Anthropic{BaseURL: baseURL, APIKey: apiKey}, nil
	case "azure":
		if baseURL == "" {
			return nil, fmt.Errorf("bungkus: azure needs a base URL like https://<resource>.openai.azure.com/openai/v1")
		}
		return &Azure{BaseURL: baseURL, APIKey: apiKey}, nil
	case "byteplus":
		return &BytePlus{BaseURL: baseURL, APIKey: apiKey}, nil
	case "deepseek":
		return &DeepSeek{BaseURL: baseURL, APIKey: apiKey}, nil
	}
	return nil, fmt.Errorf("bungkus: unknown provider %q", provider)
}

// chatCompletions calls an OpenAI-compatible /chat/completions endpoint.
func chatCompletions(ctx context.Context, hc *http.Client, name, baseURL string, headers map[string]string, model string, msgs []Message) (string, error) {
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	body := map[string]any{"model": model, "messages": msgs}
	if err := post(ctx, hc, name, baseURL+"/chat/completions", headers, body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("bungkus: %s returned no choices", name)
	}
	return out.Choices[0].Message.Content, nil
}

func post(ctx context.Context, hc *http.Client, name, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bungkus: %s %d: %s", name, resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

func baseOr(baseURL, def string) string {
	if baseURL == "" {
		return def
	}
	return strings.TrimRight(baseURL, "/")
}
