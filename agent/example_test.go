package agent_test

import (
	"context"
	"fmt"
	"os"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/agent"
	"github.com/jerichosiahaya/bungkus/tool"
)

func ExampleReAct() {
	type weatherArgs struct {
		City string `json:"city" desc:"City name"`
	}
	weather := tool.New("get_weather", "Current weather for a city",
		func(_ context.Context, a weatherArgs) (string, error) {
			return "31°C and sunny in " + a.City, nil
		})

	a := &agent.ReAct{
		Provider: bungkus.NewAnthropic(os.Getenv("ANTHROPIC_API_KEY")),
		Request:  bungkus.ChatRequest{Model: "claude-sonnet-5-5"},
		Tools:    []tool.Invokable{weather},
	}
	res, err := a.Run(context.Background(), []bungkus.Message{{Role: "user", Content: "Weather in Bali?"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(res.Answer())
}
