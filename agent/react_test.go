package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/tool"
)

// fakeModel replies with the scripted responses in order and records requests.
type fakeModel struct {
	replies []*bungkus.ChatResponse
	reqs    []bungkus.ChatRequest
}

func (f *fakeModel) Chat(_ context.Context, req *bungkus.ChatRequest) (*bungkus.ChatResponse, error) {
	f.reqs = append(f.reqs, *req)
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}

// ChatStream streams the next reply: its reasoning and text one rune at a
// time, then a final chunk with the tool calls and usage.
func (f *fakeModel) ChatStream(ctx context.Context, req *bungkus.ChatRequest) iter.Seq2[*bungkus.ChatResponse, error] {
	return func(yield func(*bungkus.ChatResponse, error) bool) {
		r, err := f.Chat(ctx, req)
		if err != nil {
			yield(nil, err)
			return
		}
		for _, c := range r.Reasoning {
			if !yield(&bungkus.ChatResponse{Message: bungkus.Message{Reasoning: string(c)}}, nil) {
				return
			}
		}
		for _, c := range r.Content {
			if !yield(&bungkus.ChatResponse{Message: bungkus.Message{Content: string(c)}}, nil) {
				return
			}
		}
		yield(&bungkus.ChatResponse{Message: bungkus.Message{ToolCalls: r.ToolCalls}, Usage: r.Usage}, nil)
	}
}

func callWeather(id string) *bungkus.ChatResponse {
	return &bungkus.ChatResponse{
		Message: bungkus.Message{Role: "assistant", ToolCalls: []bungkus.ToolCall{{ID: id, Name: "weather", Arguments: `{"city":"Bali"}`}}},
		Usage:   bungkus.Usage{TotalTokens: 10},
	}
}

var weather = tool.New("weather", "", func(_ context.Context, a struct {
	City string `json:"city"`
}) (string, error) {
	return "sunny in " + a.City, nil
})

func TestReAct(t *testing.T) {
	model := &fakeModel{replies: []*bungkus.ChatResponse{
		callWeather("c1"),
		{Message: bungkus.Message{Role: "assistant", Content: "It's sunny."}, Usage: bungkus.Usage{TotalTokens: 5}},
	}}
	a := &ReAct{
		Provider: model,
		Request:  bungkus.ChatRequest{Model: "m", Messages: []bungkus.Message{{Role: "system", Content: "be brief"}}},
		Tools:    []tool.Invokable{weather},
	}
	res, err := a.Run(context.Background(), []bungkus.Message{{Role: "user", Content: "Weather in Bali?"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer() != "It's sunny." || len(res.Messages) != 3 || res.Usage.TotalTokens != 15 {
		t.Errorf("result: %+v", res)
	}
	if tr := res.Messages[1]; tr.Role != "tool" || tr.ToolCallID != "c1" || tr.Content != "sunny in Bali" {
		t.Errorf("tool result: %+v", tr)
	}
	second := model.reqs[1]
	if len(second.Messages) != 4 || second.Messages[0].Role != "system" || second.Messages[3].Role != "tool" {
		t.Errorf("second request messages: %+v", second.Messages)
	}
	if len(second.Tools) != 1 || second.Tools[0].Name != "weather" || second.Model != "m" {
		t.Errorf("second request: %+v", second)
	}
	if len(a.Request.Messages) != 1 || len(a.Request.Tools) != 0 {
		t.Error("Run mutated the request template")
	}
}

func TestReActMaxSteps(t *testing.T) {
	model := &fakeModel{replies: []*bungkus.ChatResponse{callWeather("1"), callWeather("2")}}
	a := &ReAct{Provider: model, Tools: []tool.Invokable{weather}, MaxSteps: 2}
	res, err := a.Run(context.Background(), []bungkus.Message{{Role: "user", Content: "loop"}})
	if !errors.Is(err, ErrMaxSteps) || len(res.Messages) != 4 {
		t.Errorf("got %v, %d messages", err, len(res.Messages))
	}
}

type errModel struct{ fakeModel }

func (*errModel) Chat(context.Context, *bungkus.ChatRequest) (*bungkus.ChatResponse, error) {
	return nil, errors.New("model down")
}

func (*errModel) ChatStream(context.Context, *bungkus.ChatRequest) iter.Seq2[*bungkus.ChatResponse, error] {
	return func(yield func(*bungkus.ChatResponse, error) bool) { yield(nil, errors.New("model down")) }
}

func TestReActErrors(t *testing.T) {
	if _, err := (&ReAct{Provider: &errModel{}}).Run(context.Background(), nil); err == nil {
		t.Error("want model error")
	}

	broken := tool.New("weather", "", func(context.Context, struct{}) (string, error) { return "", errors.New("api down") })
	res, err := (&ReAct{Provider: &fakeModel{replies: []*bungkus.ChatResponse{callWeather("1")}}, Tools: []tool.Invokable{broken}}).
		Run(context.Background(), nil)
	if err == nil || len(res.Messages) != 1 {
		t.Errorf("want tool error with the assistant turn kept, got %v, %+v", err, res)
	}

	if (&Result{}).Answer() != "" {
		t.Error("empty result should have empty answer")
	}
}

func TestStream(t *testing.T) {
	model := &fakeModel{replies: []*bungkus.ChatResponse{
		callWeather("c1"),
		{Message: bungkus.Message{Role: "assistant", Content: "Sunny.", Reasoning: "ok"}, Usage: bungkus.Usage{TotalTokens: 5}},
	}}
	a := &ReAct{Provider: model, Tools: []tool.Invokable{weather}}
	var log []string
	for ev, err := range a.Stream(context.Background(), []bungkus.Message{{Role: "user", Content: "Weather in Bali?"}}) {
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case ev.Text != "":
			log = append(log, "text "+ev.Text)
		case ev.Reasoning != "":
			log = append(log, "reasoning "+ev.Reasoning)
		case ev.Message != nil:
			log = append(log, fmt.Sprintf("message %s %d calls %q", ev.Message.Role, len(ev.Message.ToolCalls), ev.Message.Content))
		case ev.Result != nil:
			log = append(log, fmt.Sprintf("result %q %d tokens", ev.Result.Answer(), ev.Result.Usage.TotalTokens))
		}
	}
	want := `message assistant 1 calls ""
message tool 0 calls "sunny in Bali"
reasoning o
reasoning k
text S
text u
text n
text n
text y
text .
message assistant 0 calls "Sunny."
result "Sunny." 15 tokens`
	if got := strings.Join(log, "\n"); got != want {
		t.Errorf("events:\n%s", got)
	}
}

func TestStreamStopEarly(t *testing.T) {
	answer := &bungkus.ChatResponse{Message: bungkus.Message{Role: "assistant", Content: "abc", Reasoning: "r"}}
	for stopAfter := 1; stopAfter <= 4; stopAfter++ { // stop on reasoning, text, the message event, and never
		model := &fakeModel{replies: []*bungkus.ChatResponse{callWeather("c1"), answer}}
		a := &ReAct{Provider: model, Tools: []tool.Invokable{weather}}
		n := 0
		for _, err := range a.Stream(context.Background(), nil) {
			if err != nil {
				t.Fatal(err)
			}
			if n++; n == stopAfter {
				break
			}
		}
		if n != stopAfter {
			t.Errorf("stopAfter %d: got %d events", stopAfter, n)
		}
	}
	// Stopping during the final answer's reasoning or text.
	for stopAfter := 3; stopAfter <= 4; stopAfter++ {
		model := &fakeModel{replies: []*bungkus.ChatResponse{callWeather("c1"), answer}}
		n := 0
		for range (&ReAct{Provider: model, Tools: []tool.Invokable{weather}}).Stream(context.Background(), nil) {
			if n++; n == stopAfter {
				break
			}
		}
		if n != stopAfter {
			t.Errorf("stop in answer %d: got %d events", stopAfter, n)
		}
	}
}

func TestStreamError(t *testing.T) {
	var got error
	for _, err := range (&ReAct{Provider: &errModel{}}).Stream(context.Background(), nil) {
		got = err
	}
	if got == nil {
		t.Error("want model error")
	}
}

// recorder records callback events, indenting nested runs.
type recorder struct{ events []string }

type depthKey struct{}

func (r *recorder) OnStart(ctx context.Context, info bungkus.RunInfo, _ any) context.Context {
	d, _ := ctx.Value(depthKey{}).(int)
	r.events = append(r.events, strings.Repeat("  ", d)+info.Component+"/"+info.Name)
	return context.WithValue(ctx, depthKey{}, d+1)
}
func (r *recorder) OnEnd(context.Context, bungkus.RunInfo, any) {}
func (r *recorder) OnError(_ context.Context, info bungkus.RunInfo, _ error) {
	r.events = append(r.events, "error "+info.Component)
}

// tracedModel reports its calls like the real providers do.
type tracedModel struct{ *fakeModel }

func (m tracedModel) Chat(ctx context.Context, req *bungkus.ChatRequest) (*bungkus.ChatResponse, error) {
	ctx, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentModel, Name: "fake"}, req)
	resp, err := m.fakeModel.Chat(ctx, req)
	end(resp, err)
	return resp, err
}

func TestCallbacksNest(t *testing.T) {
	rec := &recorder{}
	ctx := bungkus.WithHandlers(context.Background(), rec)
	model := tracedModel{&fakeModel{replies: []*bungkus.ChatResponse{callWeather("c1"), {Message: bungkus.Message{Content: "done"}}}}}
	if _, err := (&ReAct{Name: "travel", Provider: model, Tools: []tool.Invokable{weather}}).Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	want := "agent/travel\n  model/fake\n  tool/weather\n  model/fake"
	if got := strings.Join(rec.events, "\n"); got != want {
		t.Errorf("events:\n%s", got)
	}

	rec.events = nil
	(&ReAct{Provider: &errModel{}}).Run(ctx, nil)
	if got := strings.Join(rec.events, ","); got != "agent/react,error agent" {
		t.Errorf("error events: %s", got)
	}
}
