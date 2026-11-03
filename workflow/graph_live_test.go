package workflow_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/workflow"
)

type liveTicket struct {
	Input      string
	Category   string
	Resolution string
}

// This opt-in end-to-end test uses the LiteLLM OpenAI-compatible endpoint.
// It costs tokens and is skipped unless LITELLM_API_KEY is set and
// BUNGKUS_LIVE=1. Set LITELLM_BASE_URL and BUNGKUS_LITELLM_MODEL as needed.
func TestLiveLiteLLMWorkflow(t *testing.T) {
	if os.Getenv("BUNGKUS_LIVE") != "1" {
		t.Skip("set BUNGKUS_LIVE=1 to call LiteLLM")
	}
	key := os.Getenv("LITELLM_API_KEY")
	if key == "" {
		t.Skip("set LITELLM_API_KEY to run the LiteLLM workflow test")
	}
	baseURL := os.Getenv("LITELLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://llm.cekat.id/v1"
	}
	model := os.Getenv("BUNGKUS_LITELLM_MODEL")
	if model == "" {
		model = "azure_ai/gpt-4.1-mini"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	provider := bungkus.NewOpenAI(key, bungkus.WithBaseURL(baseURL))

	g := workflow.New[liveTicket]("support-triage-live")
	if err := g.SetMaxExecutions(4); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("classify", func(ctx context.Context, ticket *liveTicket) (string, error) {
		resp, err := provider.Chat(ctx, &bungkus.ChatRequest{
			Model: model,
			Messages: []bungkus.Message{{Role: "user", Content: fmt.Sprintf(
				"Classify this customer message as exactly one word: refund or information. Reply with only the word. Message: %s", ticket.Input)}},
		})
		if err != nil {
			return "", err
		}
		ticket.Category = strings.ToLower(strings.TrimSpace(resp.Content))
		if strings.Contains(ticket.Category, "refund") {
			ticket.Category = "refund"
			return "refund", nil
		}
		if strings.Contains(ticket.Category, "information") {
			ticket.Category = "information"
			return "information", nil
		}
		return "", fmt.Errorf("unexpected classification %q", resp.Content)
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("refund", func(_ context.Context, ticket *liveTicket) (string, error) {
		ticket.Resolution = "Refund request routed to the refunds team."
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("information", func(_ context.Context, ticket *liveTicket) (string, error) {
		ticket.Resolution = "Information request routed to customer support."
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	g.StartAt("classify")

	result, err := g.Run(ctx, liveTicket{Input: "My order arrived broken and I want my money back."})
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Category != "refund" || !strings.Contains(strings.ToLower(result.State.Resolution), "refund") {
		t.Fatalf("unexpected workflow output: %+v", result)
	}
	if got, want := strings.Join(result.Trace, ","), "classify,refund"; got != want {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}
