package tracing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/jerichosiahaya/bungkus"
)

// run simulates an agent run with one model call and one tool call.
func run(h bungkus.Handler, toolErr error) {
	ctx := bungkus.WithHandlers(context.Background(), h)
	ctx, endAgent := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentAgent, Name: "travel"}, []bungkus.Message{{Role: "user", Content: "hi"}})

	temperature, topP := 0.5, 0.9
	_, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentModel, Name: "anthropic"},
		&bungkus.ChatRequest{Model: "claude-sonnet-5-5", MaxTokens: 100, Temperature: &temperature, TopP: &topP, Messages: []bungkus.Message{{Role: "user", Content: "hi"}}})
	end(&bungkus.ChatResponse{Message: bungkus.Message{Content: "halo"}, FinishReason: "tool_calls", Usage: bungkus.Usage{PromptTokens: 12, CompletionTokens: 4}}, nil)

	_, end = bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentTool, Name: "weather"}, bungkus.ToolCall{ID: "c1", Arguments: `{"city":"Bali"}`})
	end("sunny", toolErr)

	endAgent(map[string]string{"answer": "halo"}, nil)
}

func record(opts ...Option) (*tracetest.SpanRecorder, bungkus.Handler) {
	sr := tracetest.NewSpanRecorder()
	return sr, New(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), opts...)
}

func attrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestSpans(t *testing.T) {
	sr, h := record()
	run(h, nil)
	spans := sr.Ended()
	if len(spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(spans))
	}
	model, tool, agent := spans[0], spans[1], spans[2]

	if model.Name() != "chat claude-sonnet-5-5" || tool.Name() != "execute_tool weather" || agent.Name() != "invoke_agent travel" {
		t.Errorf("names: %s, %s, %s", model.Name(), tool.Name(), agent.Name())
	}
	if model.SpanKind() != trace.SpanKindClient {
		t.Errorf("model span kind: %v", model.SpanKind())
	}
	for _, child := range []sdktrace.ReadOnlySpan{model, tool} {
		if child.Parent().SpanID() != agent.SpanContext().SpanID() {
			t.Errorf("%s is not a child of the agent span", child.Name())
		}
	}

	m := attrs(model)
	for key, want := range map[attribute.Key]any{
		"gen_ai.operation.name": "chat", "gen_ai.provider.name": "anthropic", "gen_ai.request.model": "claude-sonnet-5-5",
		"gen_ai.request.max_tokens": int64(100), "gen_ai.request.temperature": 0.5, "gen_ai.request.top_p": 0.9,
		"gen_ai.usage.input_tokens": int64(12), "gen_ai.usage.output_tokens": int64(4),
	} {
		if got := m[key].AsInterface(); got != want {
			t.Errorf("model %s: got %v, want %v", key, got, want)
		}
	}
	if got := m["gen_ai.response.finish_reasons"].AsStringSlice(); len(got) != 1 || got[0] != "tool_calls" {
		t.Errorf("finish reasons: %v", got)
	}
	if got := attrs(tool)["gen_ai.tool.call.id"].AsString(); got != "c1" {
		t.Errorf("tool call id: %q", got)
	}
	// Content is off by default.
	for _, s := range spans {
		for _, key := range []attribute.Key{"input.value", "output.value", "gen_ai.tool.call.arguments", "gen_ai.tool.call.result"} {
			if _, ok := attrs(s)[key]; ok {
				t.Errorf("%s has %s without WithContent", s.Name(), key)
			}
		}
	}
}

func TestContentAndErrors(t *testing.T) {
	sr, h := record(WithContent())
	run(h, errors.New("api down"))
	spans := sr.Ended()
	model, tool, agent := attrs(spans[0]), spans[1], attrs(spans[2])

	if !strings.Contains(model["input.value"].AsString(), `"content":"hi"`) || !strings.Contains(model["output.value"].AsString(), `"content":"halo"`) {
		t.Errorf("model content: %v / %v", model["input.value"], model["output.value"])
	}
	if attrs(tool)["gen_ai.tool.call.arguments"].AsString() != `{"city":"Bali"}` {
		t.Errorf("tool arguments: %v", attrs(tool))
	}
	if agent["output.value"].AsString() != `{"answer":"halo"}` {
		t.Errorf("agent output: %v", agent["output.value"])
	}
	if tool.Status().Code != codes.Error || tool.Status().Description != "api down" || len(tool.Events()) != 1 {
		t.Errorf("tool error not recorded: %+v", tool.Status())
	}

	// A tool result with content on, and values that cannot be JSON.
	sr, h = record(WithContent())
	ctx := bungkus.WithHandlers(context.Background(), h)
	_, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentTool, Name: "x"}, bungkus.ToolCall{})
	end("result", nil)
	_, end = bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentAgent, Name: "a"}, func() {})
	end(nil, nil)
	if got := attrs(sr.Ended()[0])["gen_ai.tool.call.result"].AsString(); got != "result" {
		t.Errorf("tool result: %q", got)
	}
	if _, ok := attrs(sr.Ended()[1])["input.value"]; ok {
		t.Error("unmarshalable input should be skipped")
	}
}

func TestGlobalProviderAndOtherComponents(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	defer otel.SetTracerProvider(old)

	ctx := bungkus.WithHandlers(context.Background(), New(nil))
	_, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: "retriever", Name: "docs"}, nil)
	end(nil, nil)
	_, end = bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentModel, Name: "openai"}, nil) // no request
	end((*bungkus.ChatResponse)(nil), nil)

	if got := sr.Ended(); len(got) != 2 || got[0].Name() != "retriever docs" || got[1].Name() != "model openai" {
		t.Errorf("spans: %v", got)
	}
}
