package agent_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/agent"
)

// TestLiveLiteLLMDiscussion exercises the moderator and participant turns with
// the configured OpenAI-compatible LiteLLM gateway. It is opt-in and billable.
func TestLiveLiteLLMDiscussion(t *testing.T) {
	if os.Getenv("BUNGKUS_LIVE") != "1" {
		t.Skip("set BUNGKUS_LIVE=1 to call LiteLLM")
	}
	key := os.Getenv("LITELLM_API_KEY")
	if key == "" {
		t.Skip("set LITELLM_API_KEY to run the LiteLLM discussion test")
	}
	baseURL := os.Getenv("LITELLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://llm.cekat.id/v1"
	}
	model := os.Getenv("BUNGKUS_LITELLM_MODEL")
	if model == "" {
		model = "azure_ai/gpt-4.1-mini"
	}
	p := bungkus.NewOpenAI(key, bungkus.WithBaseURL(baseURL))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	d := &agent.Discussion{
		Goal:        "Agree on one concise benefit of writing automated tests. Once the participants' points agree, finish the discussion.",
		Coordinator: agent.DiscussionCoordinator{Provider: p, Request: bungkus.ChatRequest{Model: model}},
		Participants: []agent.DiscussionParticipant{
			{Name: "pragmatist", Provider: p, Request: bungkus.ChatRequest{Model: model, Messages: []bungkus.Message{{Role: "system", Content: "You are a concise software engineering pragmatist. State one specific benefit of automated tests."}}}},
			{Name: "maintainer", Provider: p, Request: bungkus.ChatRequest{Model: model, Messages: []bungkus.Message{{Role: "system", Content: "You are a concise open-source maintainer. Respond to prior speakers and help the group reach agreement."}}}},
		},
		MaxTurns: 4,
	}
	res, err := d.Run(ctx, []bungkus.Message{{Role: "user", Content: "Discuss the main benefit of automated tests for a small Go library."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Speakers) == 0 || len(res.Speakers) > d.MaxTurns || len(res.Transcript) < 2 {
		t.Fatalf("unexpected discussion result: %+v", res)
	}
	if res.Completed && strings.TrimSpace(res.Transcript[len(res.Transcript)-1].Content) == "" {
		t.Fatalf("completed discussion has empty last contribution: %+v", res)
	}
	fmt.Printf("discussion completed=%t speakers=%v tokens=%d\n", res.Completed, res.Speakers, res.Usage.TotalTokens)
}
