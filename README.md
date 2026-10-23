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

Tool calling, images, temperature and other sampling parameters, usage/token counts.

## License

MIT
