package bungkus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The thinking blocks a Claude model returns, including a redacted one; they
// must come back byte-for-byte in the next request.
const (
	thinkingBlock = `{"type":"thinking","thinking":"check the weather","signature":"sig-1"}`
	redactedBlock = `{"type":"redacted_thinking","data":"enc-2"}`
)

func TestReasoningRoundTrip(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		stream := body["stream"] == true
		switch r.URL.Path {
		case "/chat/completions":
			if stream {
				w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"check "}}]}` + "\n\n" +
					`data: {"choices":[{"delta":{"reasoning_content":"the weather"}}]}` + "\n\n" +
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
					"data: [DONE]\n\n"))
				return
			}
			w.Write([]byte(`{"choices":[{"message":{"reasoning_content":"check the weather","tool_calls":[{"id":"c1","type":"function","function":{"name":"weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case "/messages":
			if stream {
				w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"check "}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"the weather"}}` + "\n\n" +
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}` + "\n\n" +
					`data: {"type":"content_block_stop","index":0}` + "\n\n" +
					`data: {"type":"content_block_start","index":1,"content_block":` + redactedBlock + `}` + "\n\n" +
					`data: {"type":"content_block_stop","index":1}` + "\n\n" +
					`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"c1","name":"weather","input":{}}}` + "\n\n" +
					`data: {"type":"content_block_stop","index":2}` + "\n\n" +
					`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}` + "\n\n"))
				return
			}
			w.Write([]byte(`{"content":[` + thinkingBlock + `,` + redactedBlock + `,{"type":"tool_use","id":"c1","name":"weather","input":{}}],"stop_reason":"tool_use"}`))
		}
	}))
	defer srv.Close()

	for _, name := range []string{"deepseek", "anthropic"} {
		p, _ := New(name, "k", WithBaseURL(srv.URL))
		for _, mode := range []string{"chat", "stream"} {
			req := &ChatRequest{Model: "m", ReasoningEffort: "high", Messages: []Message{{Role: "user", Content: "weather?"}}}
			var resp *ChatResponse
			var err error
			if mode == "chat" {
				resp, err = p.Chat(context.Background(), req)
			} else {
				resp, err = Collect(p.ChatStream(context.Background(), req))
			}
			if err != nil {
				t.Fatalf("%s %s: %v", name, mode, err)
			}
			if resp.Reasoning != "check the weather" || len(resp.ToolCalls) != 1 {
				t.Errorf("%s %s: got %+v", name, mode, resp)
			}

			// Send the assistant turn back, as a tool loop does.
			bodies = nil
			req.Messages = append(req.Messages, resp.Message, Message{Role: "tool", ToolCallID: "c1", Content: "sunny"})
			p.Chat(context.Background(), req)
			sent := bodies[0]
			if name == "deepseek" {
				if got := at(sent, "messages", 1, "reasoning_content"); got != "check the weather" {
					t.Errorf("%s %s: reasoning_content not sent back: %v", name, mode, got)
				}
				if sent["reasoning_effort"] != "high" {
					t.Errorf("%s: reasoning_effort: %v", name, sent["reasoning_effort"])
				}
				continue
			}
			content := at(sent, "messages", 1, "content").([]any)
			var want0, want1 map[string]any
			json.Unmarshal([]byte(thinkingBlock), &want0)
			json.Unmarshal([]byte(redactedBlock), &want1)
			if len(content) != 3 || !jsonEqual(content[0], want0) || !jsonEqual(content[1], want1) || at(content[2], "type") != "tool_use" {
				t.Errorf("%s %s: thinking blocks not sent back unchanged: %v", name, mode, content)
			}
			if at(sent, "output_config", "effort") != "high" {
				t.Errorf("%s: effort: %v", name, sent["output_config"])
			}
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestBadReasoningData(t *testing.T) {
	_, err := messagesBody(&ChatRequest{Messages: []Message{{Role: "assistant", ReasoningData: json.RawMessage(`{"not":"a list"}`)}}})
	if err == nil {
		t.Error("want error for malformed ReasoningData")
	}
}
