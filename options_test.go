package bungkus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOptionsUsageFinish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		stream := body["stream"] == true
		switch r.URL.Path {
		case "/chat/completions":
			checks := map[string][2]any{
				"temperature": {body["temperature"], 0.0},
				"top_p":       {body["top_p"], 0.9},
				"stop":        {at(body, "stop", 0), "END"},
				"tool_choice": {at(body, "tool_choice", "function", "name"), "weather"},
			}
			for name, c := range checks {
				if c[0] != c[1] {
					t.Errorf("chat completions %s: got %v, want %v", name, c[0], c[1])
				}
			}
			if stream && at(body, "stream_options", "include_usage") != true {
				t.Error("stream must ask for usage")
			}
			if stream {
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n" +
					`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" +
					`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}` + "\n\n" +
					"data: [DONE]\n\n"))
				return
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
		case "/messages":
			checks := map[string][2]any{
				"temperature":    {body["temperature"], 0.0},
				"top_p":          {body["top_p"], 0.9},
				"stop_sequences": {at(body, "stop_sequences", 0), "END"},
				"tool_choice":    {at(body, "tool_choice", "name"), "weather"},
				"tool_choice ty": {at(body, "tool_choice", "type"), "tool"},
			}
			for name, c := range checks {
				if c[0] != c[1] {
					t.Errorf("messages %s: got %v, want %v", name, c[0], c[1])
				}
			}
			// 4 uncached + 6 cache-read input tokens = 10 prompt tokens.
			if stream {
				w.Write([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":4,"cache_read_input_tokens":6,"output_tokens":1}}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
					`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":5}}` + "\n\n" +
					`data: {"type":"message_stop"}` + "\n\n"))
				return
			}
			w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"max_tokens","usage":{"input_tokens":4,"cache_read_input_tokens":6,"output_tokens":5}}`))
		}
	}))
	defer srv.Close()

	zero, topP := 0.0, 0.9
	req := &ChatRequest{
		Model: "m", Messages: []Message{{Role: "user", Content: "hello"}},
		Temperature: &zero, TopP: &topP, Stop: []string{"END"}, ToolChoice: "weather",
		Tools: []Tool{{Name: "weather"}},
	}
	want := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	for _, name := range []string{"openai", "anthropic", "azure", "byteplus", "deepseek"} {
		p, _ := New(name, "k", WithBaseURL(srv.URL))

		resp, err := p.Chat(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.Content != "hi" || resp.FinishReason != FinishLength || resp.Usage != want {
			t.Errorf("%s chat: got %+v", name, resp)
		}

		var last *ChatResponse
		for chunk, err := range p.ChatStream(context.Background(), req) {
			if err != nil {
				t.Fatalf("%s stream: %v", name, err)
			}
			last = chunk
		}
		if last.FinishReason != FinishLength || last.Usage != want {
			t.Errorf("%s stream final: got %+v", name, last)
		}
	}
}

func TestToolChoiceMapping(t *testing.T) {
	cc := func(r *ChatRequest) map[string]any { b, _ := (&client{}).chatCompletionsBody(r); return b }
	messagesBody := func(r *ChatRequest) map[string]any { b, _ := messagesBody(r); return b }
	for choice, want := range map[string]any{"auto": "auto", "none": "none", "required": "required"} {
		if got := cc(&ChatRequest{ToolChoice: choice})["tool_choice"]; got != want {
			t.Errorf("chat completions %q: got %v", choice, got)
		}
	}
	for choice, want := range map[string]string{"auto": "auto", "none": "none", "required": "any", "x": "tool"} {
		if got := at(messagesBody(&ChatRequest{ToolChoice: choice}), "tool_choice", "type"); got != want {
			t.Errorf("messages %q: got %v", choice, got)
		}
	}
	if _, ok := cc(&ChatRequest{})["tool_choice"]; ok {
		t.Error("empty ToolChoice should send nothing")
	}
}
