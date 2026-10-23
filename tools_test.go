package bungkus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// at walks decoded JSON by map keys and slice indexes.
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			s, _ := v.([]any)
			if k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

func TestToolCalling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		stream := body["stream"] == true
		switch r.URL.Path {
		case "/chat/completions":
			checks := map[string][2]any{
				"tool name":     {at(body, "tools", 0, "function", "name"), "weather"},
				"tool schema":   {at(body, "tools", 0, "function", "parameters", "type"), "object"},
				"call id":       {at(body, "messages", 1, "tool_calls", 0, "id"), "c1"},
				"call args":     {at(body, "messages", 1, "tool_calls", 0, "function", "arguments"), `{"city":"Bali"}`},
				"call content":  {at(body, "messages", 1, "content"), nil},
				"result for id": {at(body, "messages", 2, "tool_call_id"), "c1"},
			}
			for name, c := range checks {
				if c[0] != c[1] {
					t.Errorf("chat completions %s: got %v, want %v", name, c[0], c[1])
				}
			}
			if stream {
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n" +
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"weather","arguments":""}}]}}]}` + "\n\n" +
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}` + "\n\n" +
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Jakarta\"}"}}]}}]}` + "\n\n" +
					"data: [DONE]\n\n"))
				return
			}
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok","tool_calls":[{"id":"c2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Jakarta\"}"}}]}}]}`))
		case "/messages":
			checks := map[string][2]any{
				"tool name":      {at(body, "tools", 0, "name"), "weather"},
				"tool schema":    {at(body, "tools", 0, "input_schema", "type"), "object"},
				"tool_use type":  {at(body, "messages", 1, "content", 0, "type"), "tool_use"},
				"tool_use input": {at(body, "messages", 1, "content", 0, "input", "city"), "Bali"},
				"result role":    {at(body, "messages", 2, "role"), "user"},
				"result id":      {at(body, "messages", 2, "content", 0, "tool_use_id"), "c1"},
				"result content": {at(body, "messages", 2, "content", 0, "content"), "sunny"},
			}
			for name, c := range checks {
				if c[0] != c[1] {
					t.Errorf("messages %s: got %v, want %v", name, c[0], c[1])
				}
			}
			if stream {
				w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
					`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c2","name":"weather","input":{}}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Jakarta\"}"}}` + "\n\n" +
					`data: {"type":"message_stop"}` + "\n\n"))
				return
			}
			w.Write([]byte(`{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"c2","name":"weather","input":{"city":"Jakarta"}}]}`))
		}
	}))
	defer srv.Close()

	req := &ChatRequest{
		Model: "m",
		Tools: []Tool{{Name: "weather", Description: "Current weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)}},
		Messages: []Message{
			{Role: "user", Content: "Weather in Bali?"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "weather", Arguments: `{"city":"Bali"}`}}},
			{Role: "tool", ToolCallID: "c1", Content: "sunny"},
		},
	}
	check := func(name, mode string, content string, calls []ToolCall) {
		if content != "ok" || len(calls) != 1 || calls[0].ID != "c2" || calls[0].Name != "weather" || calls[0].Arguments != `{"city":"Jakarta"}` {
			t.Errorf("%s %s: got content %q, calls %+v", name, mode, content, calls)
		}
	}
	for _, name := range []string{"openai", "anthropic", "azure", "byteplus", "deepseek"} {
		p, _ := New(name, "k", WithBaseURL(srv.URL))

		resp, err := p.Chat(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		check(name, "chat", resp.Content, resp.ToolCalls)

		var content string
		var calls []ToolCall
		for chunk, err := range p.ChatStream(context.Background(), req) {
			if err != nil {
				t.Fatalf("%s stream: %v", name, err)
			}
			content += chunk.Content
			calls = append(calls, chunk.ToolCalls...)
		}
		check(name, "stream", content, calls)
	}
}

func TestMessagesBodyGroupsToolResults(t *testing.T) {
	body := messagesBody(&ChatRequest{Messages: []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "x"}, {ID: "b", Name: "y"}}},
		{Role: "tool", ToolCallID: "a", Content: "1"},
		{Role: "tool", ToolCallID: "b", Content: "2"},
		{Role: "user", Content: "thanks"},
	}})
	msgs := body["messages"].([]anthMessage)
	if len(msgs) != 4 || len(msgs[2].Content) != 2 || msgs[3].Content[0].Text != "thanks" {
		t.Errorf("tool results not grouped: %+v", msgs)
	}
	if string(msgs[1].Content[0].Input) != "{}" {
		t.Errorf("empty arguments should become {}: %s", msgs[1].Content[0].Input)
	}
}
