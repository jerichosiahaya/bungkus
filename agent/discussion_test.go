package agent

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/tool"
)

type discussionModel struct {
	replies  []*bungkus.ChatResponse
	requests []bungkus.ChatRequest
}

func (m *discussionModel) Chat(_ context.Context, req *bungkus.ChatRequest) (*bungkus.ChatResponse, error) {
	m.requests = append(m.requests, *req)
	if len(m.replies) == 0 {
		return nil, errors.New("unexpected extra chat call")
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}
func (m *discussionModel) ChatStream(context.Context, *bungkus.ChatRequest) iter.Seq2[*bungkus.ChatResponse, error] {
	return func(yield func(*bungkus.ChatResponse, error) bool) { yield(nil, errors.New("unused")) }
}

func reply(content string, tokens int) *bungkus.ChatResponse {
	return &bungkus.ChatResponse{Message: bungkus.Message{Role: "assistant", Content: content}, Usage: bungkus.Usage{TotalTokens: tokens}}
}

func TestDiscussionUntilGoalComplete(t *testing.T) {
	moderator := &discussionModel{replies: []*bungkus.ChatResponse{reply("planner", 2), reply("reviewer", 2), reply("DONE", 2)}}
	planner := &discussionModel{replies: []*bungkus.ChatResponse{reply("We should split work into small steps.", 5)}}
	reviewer := &discussionModel{replies: []*bungkus.ChatResponse{reply("Agreed; that meets the goal.", 7)}}
	d := &Discussion{
		Goal:        "Agree on a concise project plan",
		Coordinator: DiscussionCoordinator{Provider: moderator, Request: bungkus.ChatRequest{Model: "mod"}},
		Participants: []DiscussionParticipant{
			{Name: "planner", Provider: planner, Request: bungkus.ChatRequest{Model: "p", Messages: []bungkus.Message{{Role: "system", Content: "Plan carefully."}}}},
			{Name: "reviewer", Provider: reviewer, Request: bungkus.ChatRequest{Model: "r"}},
		},
	}
	res, err := d.Run(context.Background(), []bungkus.Message{{Role: "user", Content: "Make a plan"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || !reflect.DeepEqual(res.Speakers, []string{"planner", "reviewer"}) || len(res.Transcript) != 3 || res.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(planner.requests[0].Messages[1].Content, "Make a plan") || !strings.Contains(planner.requests[0].Messages[1].Content, "Goal: Agree on a concise project plan") {
		t.Errorf("planner prompt did not include initial message/goal: %+v", planner.requests[0].Messages)
	}
	if len(reviewer.requests[0].Messages) != 1 || !strings.Contains(reviewer.requests[0].Messages[0].Content, "planner: We should split work into small steps.") {
		t.Errorf("reviewer did not get the shared transcript: %+v", reviewer.requests[0].Messages)
	}
}

func TestDiscussionParallelTurnBatch(t *testing.T) {
	moderator := &discussionModel{replies: []*bungkus.ChatResponse{reply("a, b", 1), reply("a", 1), reply("DONE", 1)}}
	a := &discussionModel{replies: []*bungkus.ChatResponse{reply("A view", 2)}}
	b := &discussionModel{replies: []*bungkus.ChatResponse{reply("B view", 3)}}
	d := &Discussion{Goal: "compare options", MaxTurns: 2, ParallelTurns: true,
		Coordinator:  DiscussionCoordinator{Provider: moderator},
		Participants: []DiscussionParticipant{{Name: "a", Provider: a}, {Name: "b", Provider: b}},
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed || !reflect.DeepEqual(res.Speakers, []string{"a", "b"}) || len(res.Transcript) != 2 || res.Usage.TotalTokens != 6 {
		t.Fatalf("unexpected parallel result: %+v", res)
	}
	if len(moderator.requests) != 1 || !strings.Contains(moderator.requests[0].Messages[0].Content, "Choose one speaker") {
		t.Fatalf("coordinator did not receive parallel selection instructions: %+v", moderator.requests)
	}
}

func TestDiscussionParticipantToolLoop(t *testing.T) {
	model := &discussionModel{replies: []*bungkus.ChatResponse{
		{Message: bungkus.Message{Role: "assistant", ToolCalls: []bungkus.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{}`}}}, Usage: bungkus.Usage{TotalTokens: 2}},
		reply("The lookup confirms the plan is feasible.", 3),
	}}
	lookup := tool.New("lookup", "look up a fact", func(context.Context, struct{}) (string, error) { return "fact", nil })
	moderator := &discussionModel{replies: []*bungkus.ChatResponse{reply("researcher", 1), reply("DONE", 1)}}
	d := &Discussion{Goal: "assess feasibility", Coordinator: DiscussionCoordinator{Provider: moderator},
		Participants: []DiscussionParticipant{{Name: "researcher", Provider: model, Tools: []tool.Invokable{lookup}}}}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || len(res.Transcript) != 1 || res.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(model.requests) != 2 || len(model.requests[0].Tools) != 1 || len(model.requests[1].Messages) < 3 || model.requests[1].Messages[len(model.requests[1].Messages)-1].Content != "fact" {
		t.Fatalf("tool was not exposed/executed/fed back: %+v", model.requests)
	}
}

func TestDiscussionStopsAtMaxTurns(t *testing.T) {
	moderator := &discussionModel{replies: []*bungkus.ChatResponse{reply("a", 1), reply("b", 1)}}
	a := &discussionModel{replies: []*bungkus.ChatResponse{reply("first", 3)}}
	b := &discussionModel{replies: []*bungkus.ChatResponse{reply("second", 4)}}
	d := &Discussion{Goal: "goal", MaxTurns: 2, Coordinator: DiscussionCoordinator{Provider: moderator}, Participants: []DiscussionParticipant{{Name: "a", Provider: a}, {Name: "b", Provider: b}}}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed || res.Usage.TotalTokens != 9 || !reflect.DeepEqual(res.Speakers, []string{"a", "b"}) {
		t.Fatalf("unexpected capped result: %+v", res)
	}
}

func TestDiscussionRejectsUnknownSpeaker(t *testing.T) {
	d := &Discussion{Goal: "goal", Coordinator: DiscussionCoordinator{Provider: &discussionModel{replies: []*bungkus.ChatResponse{reply("ghost", 0)}}}, Participants: []DiscussionParticipant{{Name: "one", Provider: &discussionModel{}}}}
	res, err := d.Run(context.Background(), nil)
	if err == nil || res == nil || !strings.Contains(err.Error(), "unknown participant") {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}

func TestDiscussionValidationAndPartialError(t *testing.T) {
	if _, err := (&Discussion{}).Run(context.Background(), nil); err == nil {
		t.Fatal("expected validation error")
	}
	participant := &discussionModel{replies: []*bungkus.ChatResponse{reply("turn", 1)}}
	moderator := &discussionModel{replies: []*bungkus.ChatResponse{reply("one", 2), nil}}
	// Make the second coordinator call fail after a participant has spoken.
	failing := &failingDiscussionModel{first: moderator}
	d := &Discussion{Goal: "goal", Coordinator: DiscussionCoordinator{Provider: failing}, Participants: []DiscussionParticipant{{Name: "one", Provider: participant}}}
	res, err := d.Run(context.Background(), nil)
	if err == nil || res == nil || len(res.Transcript) != 1 || res.Usage.TotalTokens != 3 {
		t.Fatalf("partial result=%+v err=%v", res, err)
	}
}

type failingDiscussionModel struct{ first *discussionModel }

func (m *failingDiscussionModel) Chat(ctx context.Context, req *bungkus.ChatRequest) (*bungkus.ChatResponse, error) {
	if len(m.first.requests) == 0 {
		return m.first.Chat(ctx, req)
	}
	return nil, errors.New("coordinator unavailable")
}
func (*failingDiscussionModel) ChatStream(context.Context, *bungkus.ChatRequest) iter.Seq2[*bungkus.ChatResponse, error] {
	return func(yield func(*bungkus.ChatResponse, error) bool) { yield(nil, errors.New("unused")) }
}
