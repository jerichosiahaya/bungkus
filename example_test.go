package bungkus_test

import (
	"context"
	"fmt"
	"os"

	"github.com/jerichosiahaya/bungkus"
)

func Example() {
	p := bungkus.NewAnthropic(os.Getenv("ANTHROPIC_API_KEY"))
	resp, err := p.Chat(context.Background(), &bungkus.ChatRequest{
		Model:    "claude-sonnet-5-5",
		Messages: []bungkus.Message{{Role: "user", Content: "halo"}},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.Content)
}

func ExampleAnthropic_ChatStream() {
	p := bungkus.NewAnthropic(os.Getenv("ANTHROPIC_API_KEY"))
	req := &bungkus.ChatRequest{
		Model:    "claude-sonnet-5-5",
		Messages: []bungkus.Message{{Role: "user", Content: "halo"}},
	}
	for chunk, err := range p.ChatStream(context.Background(), req) {
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Print(chunk.Content)
	}
}

func ExampleChatRequest_options() {
	temperature := 0.2
	_ = &bungkus.ChatRequest{
		Model:       "deepseek-chat",
		Messages:    []bungkus.Message{{Role: "user", Content: "halo"}},
		Temperature: &temperature,
		ToolChoice:  "auto",
	}
}

func ExampleOpenAI_Decide() {
	d := bungkus.NewOpenAI(os.Getenv("OPENAI_API_KEY"))
	resp, err := d.Decide(context.Background(), &bungkus.DecideRequest{
		Model: "gpt-6-luna",
		Input: "The package arrived with a broken screen. I want my money back.",
		Questions: []bungkus.Question{
			{Name: "damaged", Type: bungkus.QuestionYesNo, Instructions: "Does the customer report a damaged item?"},
			{Name: "queue", Type: bungkus.QuestionChoice, Instructions: "Which team should handle this?", Choices: []bungkus.Choice{
				{Value: "billing", Description: "Refunds and payments"},
				{Value: "shipping", Description: "Delivery and damaged items"},
			}},
			{Name: "urgency", Type: bungkus.QuestionScore, Instructions: "How urgent is this?", Choices: []bungkus.Choice{
				{Value: "low"}, {Value: "medium"}, {Value: "high"}, // levels, lowest first
			}},
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	damaged, _ := resp.Answer("damaged")
	queue, _ := resp.Answer("queue")
	urgency, _ := resp.Answer("urgency")
	fmt.Printf("damaged %.2f, queue %s (%.2f), urgency %.1f of 2\n", damaged.Probability, queue.Choice, queue.Confidence, urgency.Score)
}

func ExampleSystemOne_Decide() {
	// The endpoint differs by vendor; this is AIML API's.
	d := bungkus.NewSystemOne("https://api.aimlapi.com/v1/decisions", os.Getenv("AIMLAPI_KEY"))
	resp, err := d.Decide(context.Background(), &bungkus.DecideRequest{
		Model: "typesafe/jev",
		Input: "My payments have failed for three days and nobody answers support.",
		Questions: []bungkus.Question{
			{Name: "is_urgent", Type: bungkus.QuestionYesNo, Instructions: "Does this message convey urgency?"}, // System One needs a Name
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("urgent: %.2f\n", resp.Answers[0].Probability)
}
