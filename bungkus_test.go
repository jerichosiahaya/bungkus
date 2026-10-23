package bungkus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/messages":
			if r.Header.Get("x-api-key") != "k" || body["system"] != "be nice" || body["max_tokens"] != 100.0 {
				t.Errorf("bad anthropic request: %v %v", r.Header, body)
			}
			if msgs := body["messages"].([]any); len(msgs) != 1 {
				t.Errorf("system message not stripped: %v", msgs)
			}
			w.Write([]byte(`{"content":[{"type":"text","text":"hi"}]}`))
		case "/chat/completions":
			if r.Header.Get("Authorization") != "Bearer k" && r.Header.Get("api-key") != "k" {
				t.Errorf("missing auth: %v", r.Header)
			}
			if body["max_tokens"] != 100.0 && body["max_completion_tokens"] != 100.0 {
				t.Errorf("max tokens not sent: %v", body)
			}
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		default:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()

	req := &ChatRequest{Model: "m", MaxTokens: 100, Messages: []Message{{"system", "be nice"}, {"user", "hello"}}}
	for _, name := range []string{"openai", "anthropic", "azure", "byteplus", "deepseek"} {
		p, err := New(name, "k", WithBaseURL(srv.URL+"/"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Chat(context.Background(), req)
		if err != nil || got.Content != "hi" {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}

	_, err := NewOpenAI("k", WithBaseURL(srv.URL+"/limited")).Chat(context.Background(), req)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Errorf("want 429 APIError, got %v", err)
	}
	if _, err := New("azure", "k"); err == nil {
		t.Error("azure without base URL should fail")
	}
	if _, err := New("nope", "k"); err == nil {
		t.Error("unknown provider should fail")
	}
}
