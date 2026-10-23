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
