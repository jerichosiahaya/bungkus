package bungkus

import (
	"context"
	"encoding/json"
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
			if r.Header.Get("x-api-key") != "k" || body["system"] != "be nice" || body["max_tokens"] == nil {
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
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		}
	}))
	defer srv.Close()

	msgs := []Message{{"system", "be nice"}, {"user", "hello"}}
	for _, p := range []string{"openai", "anthropic", "azure", "byteplus", "deepseek"} {
		c, _ := New(p, srv.URL, "k")
		got, err := c.Chat(context.Background(), "m", msgs)
		if err != nil || got != "hi" {
			t.Errorf("%s: got %q, %v", p, got, err)
		}
	}
	if _, err := New("azure", "", "k"); err == nil {
		t.Error("azure without base URL should fail")
	}
}
