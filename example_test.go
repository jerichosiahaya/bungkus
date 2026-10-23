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
