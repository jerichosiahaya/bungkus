package bungkus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestParts(t *testing.T) {
	png := DataURL("image/png", []byte{1, 2, 3})
	if png != "data:image/png;base64,AQID" {
		t.Fatalf("DataURL: %s", png)
	}
	msgs := []Message{{Role: "user", Content: "what is this?", Parts: []Part{
		{Type: PartImage, URL: png},
		{Type: PartImage, URL: "https://x/cat.jpg"},
		{Type: PartFile, URL: DataURL("application/pdf", []byte("%PDF")), Name: "a.pdf"},
	}}}

	cc, err := (&client{}).chatCompletionsBody(&ChatRequest{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	var got any
	b, _ := json.Marshal(cc["messages"])
	json.Unmarshal(b, &got)
	for path, want := range map[string]any{
		"text":     at(got, 0, "content", 0, "text") == "what is this?",
		"image":    at(got, 0, "content", 1, "image_url", "url") == png,
		"url":      at(got, 0, "content", 2, "image_url", "url") == "https://x/cat.jpg",
		"file":     at(got, 0, "content", 3, "file", "filename") == "a.pdf",
		"filedata": strings.HasPrefix(at(got, 0, "content", 3, "file", "file_data").(string), "data:application/pdf;base64,"),
	} {
		if want != true {
			t.Errorf("chat completions %s: %v", path, got)
		}
	}

	mb, err := messagesBody(&ChatRequest{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(mb["messages"])
	json.Unmarshal(b, &got)
	for path, want := range map[string]any{
		"base64": at(got, 0, "content", 1, "source", "type") == "base64" && at(got, 0, "content", 1, "source", "media_type") == "image/png" && at(got, 0, "content", 1, "source", "data") == "AQID",
		"url":    at(got, 0, "content", 2, "source", "url") == "https://x/cat.jpg",
		"doc":    at(got, 0, "content", 3, "type") == "document" && at(got, 0, "content", 3, "title") == "a.pdf",
	} {
		if want != true {
			t.Errorf("messages %s: %v", path, got)
		}
	}

	bad := []Message{{Role: "user", Parts: []Part{{Type: "video"}}}}
	if _, err := (&client{}).chatCompletionsBody(&ChatRequest{Messages: bad}); err == nil {
		t.Error("chat completions: want error for unknown part type")
	}
	if _, err := messagesBody(&ChatRequest{Messages: bad}); err == nil {
		t.Error("messages: want error for unknown part type")
	}
	if _, _, ok := parseDataURL("data:text/plain,hello"); ok {
		t.Error("non-base64 data URL should not parse")
	}
}

func TestResponseFormat(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	cc := func(f *ResponseFormat) any {
		b, _ := (&client{}).chatCompletionsBody(&ChatRequest{ResponseFormat: f})
		return b["response_format"]
	}
	if got := cc(&ResponseFormat{}); at(got, "type") != "json_object" {
		t.Errorf("json mode: %v", got)
	}
	if got := cc(&ResponseFormat{Schema: schema}); at(got, "json_schema", "name") != "response" || at(got, "json_schema", "strict") != true {
		t.Errorf("json schema: %v", got)
	}
	if got := cc(&ResponseFormat{Name: "invoice", Schema: schema}); at(got, "json_schema", "name") != "invoice" {
		t.Errorf("schema name: %v", got)
	}

	mb, err := messagesBody(&ChatRequest{ResponseFormat: &ResponseFormat{Schema: schema}})
	if err != nil || at(mb, "output_config", "format", "type") != "json_schema" {
		t.Errorf("anthropic schema: %v, %v", mb["output_config"], err)
	}
	if _, err := messagesBody(&ChatRequest{ResponseFormat: &ResponseFormat{}}); err == nil {
		t.Error("anthropic without schema should fail")
	}
	// Body errors surface from Chat and ChatStream without a request.
	p := NewAnthropic("k", WithBaseURL("http://unused"))
	if _, err := p.Chat(context.Background(), &ChatRequest{ResponseFormat: &ResponseFormat{}}); err == nil {
		t.Error("Chat should return the body error")
	}
	if _, err := Collect(p.ChatStream(context.Background(), &ChatRequest{ResponseFormat: &ResponseFormat{}})); err == nil {
		t.Error("ChatStream should return the body error")
	}
	bad := &ChatRequest{Messages: []Message{{Role: "user", Parts: []Part{{Type: "video"}}}}}
	o := NewOpenAI("k", WithBaseURL("http://unused"))
	if _, err := o.Chat(context.Background(), bad); err == nil {
		t.Error("chat completions Chat should return the body error")
	}
	if _, err := Collect(o.ChatStream(context.Background(), bad)); err == nil {
		t.Error("chat completions ChatStream should return the body error")
	}
}
