# bungkus

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

Requires Go 1.23+ for range-over-func streaming.

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

Breaking out of the loop closes the connection.

### Tool calling

```go
req := &bungkus.ChatRequest{
	Model: "gpt-5",
	Tools: []bungkus.Tool{{
		Name:        "get_weather",
		Description: "Current weather for a city",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
	}},
	Messages: []bungkus.Message{{Role: "user", Content: "Weather in Bali?"}},
}

for {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		log.Fatal(err)
	}
	req.Messages = append(req.Messages, resp.Message)
	if len(resp.ToolCalls) == 0 {
		fmt.Println(resp.Content)
		break
	}
	for _, call := range resp.ToolCalls {
		result := runTool(call.Name, call.Arguments) // your code; Arguments is a JSON object
		req.Messages = append(req.Messages, bungkus.Message{Role: "tool", ToolCallID: call.ID, Content: result})
	}
}
```

The same `Message` history works for every provider: for Anthropic, bungkus converts
tool calls to `tool_use` blocks and groups consecutive tool results into one user turn.
With `ChatStream`, text arrives as deltas and the complete `ToolCalls` arrive in the last chunk.

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

Images, temperature and other sampling parameters, tool choice, usage/token counts.

## License

MIT
