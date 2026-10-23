package prompt

import (
	"strings"
	"testing"

	"github.com/jerichosiahaya/bungkus"
)

func TestFormat(t *testing.T) {
	tmpl := Must(New(
		bungkus.Message{Role: "system", Content: "You are a {{.role}}."},
		Placeholder("history"),
		bungkus.Message{Role: "user", Content: "{{.question}}", Parts: []bungkus.Part{{Type: bungkus.PartImage, URL: "https://x/a.png"}}},
	))
	history := []bungkus.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "halo"}}
	msgs, err := tmpl.Format(map[string]any{"role": "guide", "question": "Bali?", "history": history})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.Role+": "+m.Content)
	}
	if strings.Join(got, "|") != "system: You are a guide.|user: hi|assistant: halo|user: Bali?" || len(msgs[3].Parts) != 1 {
		t.Errorf("got %v", got)
	}

	// No history: the placeholder inserts nothing.
	if msgs, err := tmpl.Format(map[string]any{"role": "guide", "question": "Bali?"}); err != nil || len(msgs) != 2 {
		t.Errorf("without history: %v, %v", msgs, err)
	}
	if _, err := tmpl.Format(map[string]any{"role": "guide"}); err == nil {
		t.Error("missing variable should fail")
	}
	if _, err := tmpl.Format(map[string]any{"role": "guide", "question": "q", "history": "oops"}); err == nil {
		t.Error("placeholder with the wrong type should fail")
	}
}

func TestParseError(t *testing.T) {
	if _, err := New(bungkus.Message{Content: "{{.broken"}); err == nil {
		t.Error("want parse error")
	}
	defer func() {
		if recover() == nil {
			t.Error("Must should panic")
		}
	}()
	Must(New(bungkus.Message{Content: "{{"}))
}
