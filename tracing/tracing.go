// Package tracing records model calls, tool calls and agent runs as
// OpenTelemetry spans with the GenAI semantic conventions. Agent runs become
// parents of their model and tool spans. Any OTLP backend works; Langfuse
// shows the model spans as generations.
//
//	ctx = bungkus.WithHandlers(ctx, tracing.New(nil)) // nil uses the global TracerProvider
package tracing

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jerichosiahaya/bungkus"
)

type Option func(*handler)

// WithContent also records prompts, replies, tool arguments and tool results
// (as input.value and output.value, which Langfuse and Phoenix display).
// Off by default, since they may contain personal data.
func WithContent() Option {
	return func(h *handler) { h.content = true }
}

// New returns a handler that creates spans from tp, or from the global
// TracerProvider when tp is nil.
func New(tp trace.TracerProvider, opts ...Option) bungkus.Handler {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	h := &handler{tracer: tp.Tracer("github.com/jerichosiahaya/bungkus")}
	for _, o := range opts {
		o(h)
	}
	return h
}

type handler struct {
	tracer  trace.Tracer
	content bool
}

func (h *handler) OnStart(ctx context.Context, info bungkus.RunInfo, input any) context.Context {
	name, kind := info.Component+" "+info.Name, trace.SpanKindInternal
	var attrs []attribute.KeyValue
	switch info.Component {
	case bungkus.ComponentModel:
		kind = trace.SpanKindClient
		attrs = append(attrs, attribute.String("gen_ai.operation.name", "chat"), attribute.String("gen_ai.provider.name", info.Name))
		if req, ok := input.(*bungkus.ChatRequest); ok {
			name = "chat " + req.Model
			attrs = append(attrs, attribute.String("gen_ai.request.model", req.Model))
			if req.MaxTokens > 0 {
				attrs = append(attrs, attribute.Int("gen_ai.request.max_tokens", req.MaxTokens))
			}
			if req.Temperature != nil {
				attrs = append(attrs, attribute.Float64("gen_ai.request.temperature", *req.Temperature))
			}
			if req.TopP != nil {
				attrs = append(attrs, attribute.Float64("gen_ai.request.top_p", *req.TopP))
			}
			attrs = h.appendJSON(attrs, "input.value", req.Messages)
		}
	case bungkus.ComponentTool:
		name = "execute_tool " + info.Name
		attrs = append(attrs, attribute.String("gen_ai.operation.name", "execute_tool"), attribute.String("gen_ai.tool.name", info.Name))
		if call, ok := input.(bungkus.ToolCall); ok {
			attrs = append(attrs, attribute.String("gen_ai.tool.call.id", call.ID))
			if h.content {
				attrs = append(attrs, attribute.String("gen_ai.tool.call.arguments", call.Arguments))
			}
		}
	case bungkus.ComponentAgent:
		name = "invoke_agent " + info.Name
		attrs = append(attrs, attribute.String("gen_ai.operation.name", "invoke_agent"), attribute.String("gen_ai.agent.name", info.Name))
		attrs = h.appendJSON(attrs, "input.value", input)
	}
	ctx, _ = h.tracer.Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
	return ctx
}

func (h *handler) OnEnd(ctx context.Context, info bungkus.RunInfo, output any) {
	span := trace.SpanFromContext(ctx)
	var attrs []attribute.KeyValue
	switch out := output.(type) {
	case *bungkus.ChatResponse:
		if out != nil {
			attrs = append(attrs,
				attribute.StringSlice("gen_ai.response.finish_reasons", []string{out.FinishReason}),
				attribute.Int("gen_ai.usage.input_tokens", out.Usage.PromptTokens),
				attribute.Int("gen_ai.usage.output_tokens", out.Usage.CompletionTokens))
			attrs = h.appendJSON(attrs, "output.value", out.Message)
		}
	case string: // a tool result
		if h.content {
			attrs = append(attrs, attribute.String("gen_ai.tool.call.result", out))
		}
	default:
		attrs = h.appendJSON(attrs, "output.value", output)
	}
	span.SetAttributes(attrs...)
	span.End()
}

func (h *handler) OnError(ctx context.Context, info bungkus.RunInfo, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.End()
}

// appendJSON adds v as a JSON attribute when content recording is on.
func (h *handler) appendJSON(attrs []attribute.KeyValue, key string, v any) []attribute.KeyValue {
	if !h.content {
		return attrs
	}
	b, err := json.Marshal(v)
	if err != nil {
		return attrs
	}
	return append(attrs, attribute.String(key, string(b)))
}
