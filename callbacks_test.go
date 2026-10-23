package bungkus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorder logs callback events; OnStart nests depth in the context.
type recorder struct {
	mu     sync.Mutex
	events []string
}

type depthKey struct{}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, s)
}

func (r *recorder) OnStart(ctx context.Context, info RunInfo, input any) context.Context {
	d, _ := ctx.Value(depthKey{}).(int)
	r.add(fmt.Sprintf("%d start %s/%s %T", d, info.Component, info.Name, input))
	return context.WithValue(ctx, depthKey{}, d+1)
}

func (r *recorder) OnEnd(ctx context.Context, info RunInfo, output any) {
	d, _ := ctx.Value(depthKey{}).(int)
	switch o := output.(type) {
	case *ChatResponse:
		r.add(fmt.Sprintf("%d end %s/%s %q", d-1, info.Component, info.Name, o.Content))
	default:
		r.add(fmt.Sprintf("%d end %s/%s %T", d-1, info.Component, info.Name, output))
	}
}

func (r *recorder) OnError(ctx context.Context, info RunInfo, err error) {
	r.add(fmt.Sprintf("error %s/%s", info.Component, info.Name))
}

func TestCallbacks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fail") {
			w.WriteHeader(400)
			return
		}
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"ha"}}]}` + "\n\n" + `data: {"choices":[{"delta":{"content":"lo"}}]}` + "\n\n"))
	}))
	defer srv.Close()

	rec := &recorder{}
	ctx := WithHandlers(context.Background(), rec)
	p := NewOpenAI("k", WithBaseURL(srv.URL))
	req := &ChatRequest{}

	Collect(p.ChatStream(ctx, req))
	for range p.ChatStream(ctx, req) {
		break // stopping early still ends the run, with what arrived
	}
	NewOpenAI("k", WithBaseURL(srv.URL+"/fail"), WithRetries(0)).Chat(ctx, req)
	p.Chat(context.Background(), req) // no handlers: nothing recorded

	want := []string{
		"0 start model/openai *bungkus.ChatRequest", `0 end model/openai "halo"`,
		"0 start model/openai *bungkus.ChatRequest", `0 end model/openai "ha"`,
		"0 start model/openai *bungkus.ChatRequest", "error model/openai",
	}
	if strings.Join(rec.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s", strings.Join(rec.events, "\n"))
	}

	// Handlers accumulate across WithHandlers calls.
	rec2 := &recorder{}
	_, end := StartRun(WithHandlers(ctx, rec2), RunInfo{Component: ComponentTool, Name: "x"}, nil)
	end(nil, errors.New("boom"))
	if len(rec2.events) != 2 || rec.events[len(rec.events)-1] != "error tool/x" {
		t.Errorf("chained handlers: %v / %v", rec.events, rec2.events)
	}
}

func TestCollect(t *testing.T) {
	stream := func(yield func(*ChatResponse, error) bool) {
		_ = yield(&ChatResponse{Message: Message{Reasoning: "hm"}}, nil) &&
			yield(&ChatResponse{Message: Message{Content: "a"}}, nil) &&
			yield(&ChatResponse{Message: Message{Content: "b", ToolCalls: []ToolCall{{ID: "1"}}, ReasoningData: []byte(`[]`)}, FinishReason: FinishToolCalls, Usage: Usage{TotalTokens: 3}}, nil) &&
			yield(nil, errors.New("cut"))
	}
	got, err := Collect(stream)
	if err == nil || got.Content != "ab" || got.Reasoning != "hm" || len(got.ToolCalls) != 1 || string(got.ReasoningData) != "[]" ||
		got.FinishReason != FinishToolCalls || got.Usage.TotalTokens != 3 || got.Role != "assistant" {
		t.Errorf("got %+v, %v", got, err)
	}
}
