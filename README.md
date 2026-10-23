# bungkus

[![CI](https://github.com/jerichosiahaya/bungkus/actions/workflows/ci.yml/badge.svg)](https://github.com/jerichosiahaya/bungkus/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/jerichosiahaya/bungkus/badges/coverage.json)](https://github.com/jerichosiahaya/bungkus/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/jerichosiahaya/bungkus.svg)](https://pkg.go.dev/github.com/jerichosiahaya/bungkus)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

*Bungkus* is Indonesian for "wrap". A small Go client that wraps the chat APIs of
five LLM providers behind one interface. Standard library only, no dependencies.

| Provider | Constructor | Notes |
|---|---|---|
| OpenAI | `NewOpenAI(key)` | |
| Anthropic | `NewAnthropic(key)` | System messages go to the `system` field; `MaxTokens` defaults to 4096 |
| Azure OpenAI | `NewAzure(endpoint, key)` | v1 API, endpoint like `https://<resource>.openai.azure.com/openai/v1`; `Model` is the deployment name |
| BytePlus ModelArk | `NewBytePlus(key)` | `Model` is the model ID or endpoint ID |
| DeepSeek | `NewDeepSeek(key)` | |

## Install

```sh
go get github.com/jerichosiahaya/bungkus
```

Requires Go 1.25+.

## Usage

```go
p := bungkus.NewAnthropic(os.Getenv("ANTHROPIC_API_KEY"))

resp, err := p.Chat(ctx, &bungkus.ChatRequest{
	Model: "claude-sonnet-5-5",
	Messages: []bungkus.Message{
		{Role: "system", Content: "Answer briefly."},
		{Role: "user", Content: "Halo!"},
	},
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(resp.Content)
fmt.Println(resp.FinishReason, resp.Usage.TotalTokens) // "stop", "length", "tool_calls" or "content_filter"
```

### Request options

```go
req := &bungkus.ChatRequest{
	Model:       "deepseek-chat",
	Messages:    msgs,
	MaxTokens:   1024,
	Temperature: new(0.2), // pointer, since 0 is valid; new(value) needs Go 1.26+
	TopP:        new(0.9),
	Stop:        []string{"\n\n"},
	ToolChoice:  "required", // "auto", "none", "required", or a tool name
}
```

### Streaming

```go
for chunk, err := range p.ChatStream(ctx, req) {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(chunk.Content)
}
```

Breaking out of the loop closes the connection. The last chunk has no text; it
carries `FinishReason`, `Usage` and any complete `ToolCalls`.

### Tools from Go functions

`tool.New` builds the JSON Schema from a struct, so the model's arguments arrive typed:

```go
import "github.com/jerichosiahaya/bungkus/tool"

type WeatherArgs struct {
	City string `json:"city" desc:"City name"`
	Unit string `json:"unit,omitempty" enum:"celsius,fahrenheit"`
}

weather := tool.New("get_weather", "Current weather for a city",
	func(ctx context.Context, a WeatherArgs) (string, error) {
		return "31°C and sunny in " + a.City, nil
	})
```

Fields are required unless they are pointers or `omitempty`. `desc` sets a description,
`enum` lists allowed values. A non-string return value is sent to the model as JSON.

### Agents

`agent.ReAct` loops: call the model, run the tools it asks for (in parallel), feed
the results back, until it answers.

```go
import "github.com/jerichosiahaya/bungkus/agent"

a := &agent.ReAct{
	Provider: bungkus.NewAnthropic(key),
	Request: bungkus.ChatRequest{
		Model:    "claude-sonnet-5-5",
		Messages: []bungkus.Message{{Role: "system", Content: "You are a travel assistant."}},
	},
	Tools:    []tool.Invokable{weather},
	MaxSteps: 10, // model calls per Run
}

res, err := a.Run(ctx, []bungkus.Message{{Role: "user", Content: "Should I pack an umbrella for Bali?"}})
fmt.Println(res.Answer(), res.Usage.TotalTokens)
history = append(history, res.Messages...) // tool calls, tool results and the answer
```

When a tool gets bad arguments or the model names an unknown tool, the error goes
back to the model so it can retry. An error your tool function returns stops the run.

### Tool calling by hand

Without the `tool` and `agent` packages, set `Tools` and run the loop yourself:

```go
req.Tools = []bungkus.Tool{{
	Name:        "get_weather",
	Description: "Current weather for a city",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
}}

for {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		log.Fatal(err)
	}
	req.Messages = append(req.Messages, resp.Message)
	if len(resp.ToolCalls) == 0 {
		break
	}
	for _, call := range resp.ToolCalls {
		result := runTool(call.Name, call.Arguments) // Arguments is a JSON object
		req.Messages = append(req.Messages, bungkus.Message{Role: "tool", ToolCallID: call.ID, Content: result})
	}
}
```

The same `Message` history works for every provider: for Anthropic, bungkus converts
tool calls to `tool_use` blocks and groups consecutive tool results into one user turn.

### Structured output

Force a tool and read its arguments: `ToolChoice: "extract_invoice"` makes every
provider return JSON matching that tool's schema in `resp.ToolCalls[0].Arguments`.

### Choosing a provider from config

```go
p, err := bungkus.New(cfg.Provider, cfg.APIKey) // "openai", "anthropic", "azure", "byteplus", "deepseek"
```

Azure needs `bungkus.WithBaseURL(endpoint)` here.

### Options

```go
bungkus.NewDeepSeek(key,
	bungkus.WithBaseURL("https://my-proxy.example.com/v1"),
	bungkus.WithHTTPClient(&http.Client{Timeout: 60 * time.Second}),
)
```

### Errors

Non-2xx responses return `*bungkus.APIError`:

```go
var apiErr *bungkus.APIError
if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
	// back off and retry
}
```

## Using it behind your own port

bungkus is an adapter. In a hexagonal or clean-architecture app, define the
interface your core needs and let any bungkus provider satisfy it:

```go
// in your domain
type LLM interface {
	Chat(ctx context.Context, req *bungkus.ChatRequest) (*bungkus.ChatResponse, error)
}

// in main
var llm LLM = bungkus.NewOpenAI(key)
```

## Not yet supported

Images and files, reasoning/thinking output, provider-native JSON mode, agent streaming.

## Development

```sh
go test -race -cover ./...
```

Tests run against local fake servers; no API keys needed. CI runs them on Go 1.25 and
the latest Go, and publishes the coverage badge from `main`.

## License

MIT
