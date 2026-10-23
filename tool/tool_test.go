package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jerichosiahaya/bungkus"
)

type Base struct {
	ID string `json:"id"`
}

type Node2 struct {
	Child *Node2 `json:"child"`
}

type Args struct {
	Base
	City    string             `json:"city" desc:"City name"`
	Unit    string             `json:"unit,omitempty" enum:"celsius,fahrenheit"`
	Days    *int               `json:"days"`
	Tags    []string           `json:"tags,omitempty"`
	Meta    map[string]float64 `json:"meta,omitempty"`
	When    time.Time          `json:"when,omitempty"`
	Tree    Node2              `json:"tree,omitempty"`
	Skip    string             `json:"-"`
	private string
}

func TestSchema(t *testing.T) {
	var got map[string]any
	json.Unmarshal(New("x", "", func(context.Context, Args) (string, error) { return "", nil }).Info().Parameters, &got)
	var want map[string]any
	json.Unmarshal([]byte(`{
		"type": "object",
		"required": ["id", "city"],
		"properties": {
			"id":   {"type": "string"},
			"city": {"type": "string", "description": "City name"},
			"unit": {"type": "string", "enum": ["celsius", "fahrenheit"]},
			"days": {"type": "integer"},
			"tags": {"type": "array", "items": {"type": "string"}},
			"meta": {"type": "object", "additionalProperties": {"type": "number"}},
			"when": {"type": "string"},
			"tree": {"type": "object", "properties": {"child": {}}}
		}
	}`), &want)
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("schema mismatch:\n%s", g)
	}
}

func TestNewPanicsOnNonStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want panic")
		}
	}()
	New("x", "", func(context.Context, string) (string, error) { return "", nil })
}

func TestInvoke(t *testing.T) {
	type out struct {
		Temp int `json:"temp"`
	}
	tl := New("weather", "", func(_ context.Context, a Args) (out, error) { return out{len(a.City)}, nil })
	if got, err := tl.Invoke(context.Background(), `{"city":"Bali"}`); err != nil || got != `{"temp":4}` {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := tl.Invoke(context.Background(), `{"city":1}`); !errors.Is(err, ErrInvalidArguments) {
		t.Errorf("want ErrInvalidArguments, got %v", err)
	}
	if _, err := tl.Invoke(context.Background(), ""); err != nil {
		t.Errorf("empty arguments should decode as {}: %v", err)
	}
}

func TestNode(t *testing.T) {
	var running, peak atomic.Int32
	slow := New("slow", "", func(_ context.Context, a struct {
		N string `json:"n"`
	}) (string, error) {
		n := running.Add(1)
		if n > peak.Load() {
			peak.Store(n)
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return "done " + a.N, nil
	})
	boom := New("boom", "", func(context.Context, struct{}) (string, error) { return "", errors.New("kaput") })
	n := NewNode(slow, boom)

	msgs, err := n.Run(context.Background(), bungkus.Message{ToolCalls: []bungkus.ToolCall{
		{ID: "1", Name: "slow", Arguments: `{"n":"a"}`},
		{ID: "2", Name: "slow", Arguments: `{"n":"b"}`},
		{ID: "3", Name: "nope"},
		{ID: "4", Name: "slow", Arguments: `{"n":5}`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() < 2 {
		t.Error("tool calls did not run in parallel")
	}
	want := []string{"done a", "done b", `error: unknown tool "nope"`, "error: invalid tool arguments"}
	for i, m := range msgs {
		if m.Role != "tool" || m.ToolCallID != string(rune('1'+i)) || !strings.HasPrefix(m.Content, want[i]) {
			t.Errorf("msg %d: %+v", i, m)
		}
	}

	_, err = n.Run(context.Background(), bungkus.Message{ToolCalls: []bungkus.ToolCall{{ID: "1", Name: "boom"}}})
	if err == nil || !strings.Contains(err.Error(), "kaput") {
		t.Errorf("want tool error, got %v", err)
	}
	if got := n.Infos(); len(got) != 2 || got[0].Name != "slow" {
		t.Errorf("infos: %+v", got)
	}
}

func TestSchemaMoreTypes(t *testing.T) {
	type more struct {
		Flag bool
		Raw  []byte `json:"raw"`
		Any  any    `json:"any"`
		Grid [2]int `json:"grid"`
	}
	var got map[string]any
	json.Unmarshal(New("x", "", func(context.Context, *more) (string, error) { return "", nil }).Info().Parameters, &got)
	props := got["properties"].(map[string]any)
	for name, want := range map[string]any{"Flag": "boolean", "raw": "string", "any": nil, "grid": "array"} {
		if p := props[name].(map[string]any); p["type"] != want {
			t.Errorf("%s: got %v", name, p)
		}
	}
}

func TestInvokeToolError(t *testing.T) {
	tl := New("x", "", func(context.Context, struct{}) (int, error) { return 0, errors.New("down") })
	if _, err := tl.Invoke(context.Background(), "{}"); err == nil || errors.Is(err, ErrInvalidArguments) {
		t.Errorf("want plain tool error, got %v", err)
	}
}
