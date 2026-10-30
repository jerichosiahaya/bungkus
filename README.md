# bungkus

[![CI](https://github.com/jerichosiahaya/bungkus/actions/workflows/ci.yml/badge.svg)](https://github.com/jerichosiahaya/bungkus/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/jerichosiahaya/bungkus/badges/coverage.json)](https://github.com/jerichosiahaya/bungkus/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/jerichosiahaya/bungkus.svg)](https://pkg.go.dev/github.com/jerichosiahaya/bungkus)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

*Bungkus* is Indonesian for "wrap". A small Go client that wraps the chat APIs of
five LLM providers behind one interface, with tools, agents, prompt templates, RAG,
decision models and observability on top. The core package uses only the standard library; only the
optional `tracing` package depends on OpenTelemetry.

| Provider | Constructor | Notes |
|---|---|---|
| OpenAI | `NewOpenAI(key)` | |
| Anthropic | `NewAnthropic(key)` | System messages go to the `system` field; `MaxTokens` defaults to 4096; thinking blocks are kept and sent back during tool use |
| Azure OpenAI | `NewAzure(endpoint, key)` | v1 API, endpoint like `https://<resource>.openai.azure.com/openai/v1`; `Model` is the deployment name |
| BytePlus ModelArk | `NewBytePlus(key)` | `Model` is the model ID or endpoint ID |
| DeepSeek | `NewDeepSeek(key)` | `reasoning_content` is sent back, which DeepSeek requires once tools are used |

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
	ReasoningEffort: "high", // reasoning_effort, or output_config.effort for Anthropic; values depend on the model
}
```

### Reasoning

Models that think return it in `resp.Reasoning` (DeepSeek and BytePlus
`reasoning_content`, Claude thinking summaries). Claude's signed thinking blocks
are kept in `resp.ReasoningData`. Keep the whole `resp.Message` in your history:
DeepSeek rejects tool-use follow-ups without the reasoning, and Claude needs its
thinking blocks back unchanged.

### Images and files

```go
img, _ := os.ReadFile("receipt.png")
msg := bungkus.Message{Role: "user", Content: "What is the total?", Parts: []bungkus.Part{
	{Type: bungkus.PartImage, URL: bungkus.DataURL("image/png", img)},
	{Type: bungkus.PartFile, URL: bungkus.DataURL("application/pdf", pdf), Name: "invoice.pdf"},
}}
```

Images can also be `https://` URLs. Chat-completions providers (OpenAI, Azure,
BytePlus) take files only as data URLs. DeepSeek accepts text only.

### JSON output

```go
req.ResponseFormat = &bungkus.ResponseFormat{
	Name:   "invoice",
	Schema: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number"}},"required":["total"],"additionalProperties":false}`),
}
resp, _ := p.Chat(ctx, req) // resp.Content is JSON matching the schema
```

OpenAI and Anthropic need `"additionalProperties": false` on every object. A
`ResponseFormat` without a `Schema` asks for any JSON object: DeepSeek supports
only that, and Anthropic does not support it.

### Streaming

```go
for chunk, err := range p.ChatStream(ctx, req) {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(chunk.Content)
}
```

Chunks carry `Content` and `Reasoning` deltas; the last one carries the complete
`ToolCalls`, `ReasoningData`, `FinishReason` and `Usage`. `bungkus.Collect(stream)`
joins them into one response. Breaking out of the loop closes the connection.

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

`a.Stream` runs the same loop but yields events as they happen:

```go
for ev, err := range a.Stream(ctx, history) {
	switch {
	case err != nil:
		log.Fatal(err)
	case ev.Text != "":
		fmt.Print(ev.Text) // the model writing
	case ev.Message != nil && ev.Message.Role == "tool":
		fmt.Println("\n[tool]", ev.Message.Content)
	case ev.Result != nil:
		history = append(history, ev.Result.Messages...) // always the last event
	}
}
```

### Prompt templates

```go
import "github.com/jerichosiahaya/bungkus/prompt"

var tmpl = prompt.Must(prompt.New(
	bungkus.Message{Role: "system", Content: "You are a {{.role}}. Answer in {{.lang}}."},
	prompt.Placeholder("history"),
	bungkus.Message{Role: "user", Content: "{{.question}}"},
))

msgs, err := tmpl.Format(map[string]any{"role": "travel guide", "lang": "Indonesian", "question": q, "history": history})
```

Contents are `text/template`s; a variable the template uses but `Format` is not given is an error.

### Embeddings and RAG

OpenAI, Azure and BytePlus implement `bungkus.Embedder`. The `rag` package splits
text, stores embeddings in memory, and retrieves the closest chunks:

```go
import "github.com/jerichosiahaya/bungkus/rag"

store := &rag.Memory{Embedder: bungkus.NewOpenAI(key), Model: "text-embedding-3-small"}
err := store.Add(ctx, rag.Chunks("handbook.md", text, 1000, 150)...) // 1000-rune chunks, 150 overlap

docs, err := store.Retrieve(ctx, "how many leave days do I get?", 4) // most similar first, with Score
```

Or let an agent search when it needs to:

```go
a := &agent.ReAct{Provider: p, Request: bungkus.ChatRequest{Model: "gpt-5"},
	Tools: []tool.Invokable{rag.NewTool("search_handbook", "Search the employee handbook", store, 4)}}
```

`rag.Memory` scans every vector, which is fine up to about 100k chunks. Beyond that,
implement `rag.Retriever` (one method) over your vector database.

### Decision models

Decision models answer typed questions about an input with probabilities instead of
text, in one fast pass: yes/no, pick one of several choices, or score on a scale.

```go
resp, err := bungkus.NewOpenAI(key).Decide(ctx, &bungkus.DecideRequest{
	Model: "gpt-6-luna",
	Input: "The package arrived with a broken screen. I want my money back.",
	Questions: []bungkus.Question{
		{Name: "damaged", Type: bungkus.QuestionYesNo, Instructions: "Does the customer report a damaged item?"},
		{Name: "queue", Type: bungkus.QuestionChoice, Instructions: "Which team should handle this?",
			Choices: []bungkus.Choice{{Value: "billing", Description: "Refunds"}, {Value: "shipping", Description: "Damaged items"}}},
		{Name: "urgency", Type: bungkus.QuestionScore, Instructions: "How urgent is this?",
			Choices: []bungkus.Choice{{Value: "low"}, {Value: "medium"}, {Value: "high"}}}, // levels, lowest first
	},
})
damaged, _ := resp.Answer("damaged") // damaged.Probability
queue, _ := resp.Answer("queue")     // queue.Choice, queue.Confidence, queue.Probabilities
```

Two request formats exist, and both return the same `DecideResponse`:

| Format | Use | Endpoints |
|---|---|---|
| OpenAI `/decisions` | `NewOpenAI(key)`, or `WithBaseURL` for a compatible gateway | OpenAI, Vercel AI Gateway, LLM Gateway `/v1/decisions` |
| System One (Jev) | `NewSystemOne(endpointURL, key)`; every question needs a `Name`; text only | TypeSafe, AIML API, LLM Gateway `/v1/systemone`, Liquid AI, Microsoft Foundry, Baseten, Upstage |

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
	bungkus.WithRetries(3), // default 2; 0 turns retries off
)
```

Requests are retried on 408, 429, 5xx and network errors, waiting as the
`Retry-After` header says or with exponential backoff (0.5s, 1s, 2s, ... up to 8s).

### Errors

Non-2xx responses, after retries, return `*bungkus.APIError`:

```go
var apiErr *bungkus.APIError
if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
	// bad API key
}
```

## Observability

Handlers on the context see every model call, tool call, agent run, embedding,
retrieval and decision, with nested runs linked to their parent:

```go
import (
	"github.com/jerichosiahaya/bungkus/logging"
	"github.com/jerichosiahaya/bungkus/tracing"
)

ctx = bungkus.WithHandlers(ctx,
	logging.New(slog.Default()), // duration, model, finish reason, tokens
	tracing.New(nil),            // OpenTelemetry spans with GenAI attributes; nil uses the global TracerProvider
)
res, err := a.Run(ctx, history) // invoke_agent → chat / execute_tool spans
```

`tracing.New(nil, tracing.WithContent())` also records prompts, replies and tool
arguments. To send traces to Langfuse, point an OTLP HTTP exporter at
`https://cloud.langfuse.com/api/public/otel` with the header
`Authorization: Basic base64(pk-lf-...:sk-lf-...)`; the model spans appear as generations.

Write your own by implementing `bungkus.Handler` (`OnStart`, `OnEnd`, `OnError`).

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

Graph orchestration, multi-agent, embeddings for Anthropic and DeepSeek (they have no
embeddings API), Claude's `budget_tokens`
thinking (deprecated; use `ReasoningEffort`).

## Development

```sh
go test -race -cover ./...
```

Tests run against local fake servers; no API keys needed. CI runs them on Go 1.25 and
the latest Go, and publishes the coverage badge from `main`.

Live tests call the real APIs for each provider whose key is set. They cost a little,
so they only run with `BUNGKUS_LIVE=1`:

```sh
BUNGKUS_LIVE=1 OPENAI_API_KEY=... ANTHROPIC_API_KEY=... go test -run Live -v .
```

Keys: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, `BYTEPLUS_API_KEY` (with
`BUNGKUS_BYTEPLUS_MODEL`), `AZURE_OPENAI_API_KEY` (with `AZURE_OPENAI_ENDPOINT` and
`AZURE_OPENAI_DEPLOYMENT`). Override any model with `BUNGKUS_<PROVIDER>_MODEL`.

## License

MIT
