package bungkus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve returns a server that answers every request with status and body.
func serve(t *testing.T, status int, body string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func drain(p Provider) (string, *ChatResponse, error) {
	var text string
	var last *ChatResponse
	for chunk, err := range p.ChatStream(context.Background(), &ChatRequest{}) {
		if err != nil {
			return text, last, err
		}
		text += chunk.Content
		last = chunk
	}
	return text, last, nil
}

func TestAPIError(t *testing.T) {
	url := serve(t, 401, `{"error":"bad key"}`)
	for _, name := range []string{"openai", "anthropic"} {
		p, _ := New(name, "k", WithBaseURL(url))
		_, err := p.Chat(context.Background(), &ChatRequest{})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 || apiErr.Body != `{"error":"bad key"}` {
			t.Errorf("%s chat: got %v", name, err)
		}
		if want := "bungkus: " + name + ` returned 401: {"error":"bad key"}`; err.Error() != want {
			t.Errorf("%s message: got %q", name, err.Error())
		}
		if _, _, err := drain(p); !errors.As(err, &apiErr) {
			t.Errorf("%s stream: got %v", name, err)
		}
	}
}

func TestTransportErrors(t *testing.T) {
	if _, err := (&Azure{}).Chat(context.Background(), &ChatRequest{}); err == nil || !strings.Contains(err.Error(), "no base URL") {
		t.Errorf("zero Azure: got %v", err)
	}
	bad := &ChatRequest{Messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{Arguments: "{not json"}}}}}
	if _, err := NewAnthropic("k", WithBaseURL("http://x")).Chat(context.Background(), bad); err == nil {
		t.Error("invalid tool arguments JSON should fail to marshal")
	}
	if _, err := NewOpenAI("k", WithBaseURL("http://bad\x7f")).Chat(context.Background(), &ChatRequest{}); err == nil {
		t.Error("invalid URL should fail")
	}
	srv := httptest.NewServer(nil)
	srv.Close()
	if _, err := NewOpenAI("k", WithBaseURL(srv.URL)).Chat(context.Background(), &ChatRequest{}); err == nil {
		t.Error("closed server should fail")
	}
}

func TestWithHTTPClient(t *testing.T) {
	var used bool
	hc := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}
	url := serve(t, 200, `{"choices":[{"message":{"content":"hi"}}]}`)
	if _, err := NewDeepSeek("k", WithBaseURL(url), WithHTTPClient(hc)).Chat(context.Background(), &ChatRequest{}); err != nil || !used {
		t.Errorf("custom client not used: %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBadResponses(t *testing.T) {
	if _, err := NewOpenAI("k", WithBaseURL(serve(t, 200, `{"choices":[]}`))).Chat(context.Background(), &ChatRequest{}); err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Errorf("no choices: got %v", err)
	}
	for name, body := range map[string]string{
		"openai bad json":     "data: {oops\n\n",
		"openai error event":  `data: {"error":{"message":"rate limited"}}` + "\n\n",
		"anthropic bad json":  "data: {oops\n\n",
		"openai line too big": "data: " + strings.Repeat("x", 2<<20) + "\n\n",
	} {
		url := serve(t, 200, body)
		p, _ := New(strings.Fields(name)[0], "k", WithBaseURL(url))
		if _, _, err := drain(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestStreamStopEarly(t *testing.T) {
	url := serve(t, 200, `data: {"choices":[{"delta":{"content":"a"}}]}`+"\n\n"+`data: {"choices":[{"delta":{"content":"b"}}]}`+"\n\n")
	n := 0
	for range NewOpenAI("k", WithBaseURL(url)).ChatStream(context.Background(), &ChatRequest{}) {
		n++
		break
	}
	if n != 1 {
		t.Errorf("got %d chunks after break", n)
	}
}

func TestStreamToolCallEdgeCases(t *testing.T) {
	// Some OpenAI-compatible servers omit index; pieces without an id continue the last call.
	url := serve(t, 200, `data: {"choices":[{"delta":{"tool_calls":[{"id":"a","function":{"name":"x","arguments":"{\"k\":"}}]}}]}`+"\n\n"+
		`data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"1}"}}]}}]}`+"\n\n"+
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"b","function":{"name":"y","arguments":"{}"}}]}}]}`+"\n\n")
	_, last, err := drain(NewOpenAI("k", WithBaseURL(url)))
	if err != nil || len(last.ToolCalls) != 2 || last.ToolCalls[0].Arguments != `{"k":1}` || last.ToolCalls[1].Name != "y" {
		t.Errorf("no-index calls: %+v, %v", last, err)
	}

	// Anthropic sends no input_json_delta for a tool without arguments.
	url = serve(t, 200, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"now"}}`+"\n\n"+
		`data: {"type":"content_block_stop","index":0}`+"\n\n"+
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`+"\n\n")
	_, last, err = drain(NewAnthropic("k", WithBaseURL(url)))
	if err != nil || len(last.ToolCalls) != 1 || last.ToolCalls[0].Arguments != "{}" || last.FinishReason != FinishToolCalls {
		t.Errorf("no-args call: %+v, %v", last, err)
	}
}

func TestAnthFinishReason(t *testing.T) {
	for in, want := range map[string]string{
		"end_turn": FinishStop, "stop_sequence": FinishStop, "max_tokens": FinishLength,
		"tool_use": FinishToolCalls, "refusal": FinishContentFilter, "pause_turn": "pause_turn",
	} {
		if got := anthFinishReason(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestUsageAdd(t *testing.T) {
	u := Usage{1, 2, 3}
	u.Add(Usage{10, 20, 30})
	if u != (Usage{11, 22, 33}) {
		t.Errorf("got %+v", u)
	}
}

func TestMessageJSON(t *testing.T) {
	b, _ := json.Marshal(Message{Role: "user", Content: "hi"})
	if string(b) != `{"role":"user","content":"hi"}` {
		t.Errorf("got %s", b)
	}
}
