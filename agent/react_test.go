package agent

import (
	"context"
	"errors"
	"iter"
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

func (f *fakeModel) ChatStream(context.Context, *bungkus.ChatRequest) iter.Seq2[*bungkus.ChatResponse, error] {
	panic("unused")
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
