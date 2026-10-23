package bungkus

import (
	"context"
	"slices"
)

// Components a Handler can observe.
const (
	ComponentModel = "model" // input *ChatRequest, output *ChatResponse (for streams, the collected reply)
	ComponentTool  = "tool"  // input ToolCall, output string (the result sent to the model)
	ComponentAgent = "agent" // input []Message, output the agent's result (*agent.Result)
)

// RunInfo says what a callback is about.
type RunInfo struct {
	Component string // one of the Component constants
	Name      string // provider, tool or agent name
}

// Handler observes model calls, tool calls and agent runs, e.g. for logging
// or tracing. OnStart may return a derived context (to carry a span, say);
// calls made inside the run, and OnEnd or OnError, receive it. That is how
// nested runs, such as a model call inside an agent, find their parent.
type Handler interface {
	OnStart(ctx context.Context, info RunInfo, input any) context.Context
	OnEnd(ctx context.Context, info RunInfo, output any)
	OnError(ctx context.Context, info RunInfo, err error)
}

type handlersKey struct{}

// WithHandlers returns a context whose model calls, tool calls and agent runs
// are reported to hs, in addition to any handlers ctx already has.
func WithHandlers(ctx context.Context, hs ...Handler) context.Context {
	old, _ := ctx.Value(handlersKey{}).([]Handler)
	return context.WithValue(ctx, handlersKey{}, slices.Concat(old, hs))
}

// StartRun reports the start of a run to the context's handlers. Call end
// with the output, or the error, when the run finishes. Components outside
// this package (tools, agents) use it to report their runs.
func StartRun(ctx context.Context, info RunInfo, input any) (context.Context, func(output any, err error)) {
	hs, _ := ctx.Value(handlersKey{}).([]Handler)
	for _, h := range hs {
		ctx = h.OnStart(ctx, info, input)
	}
	return ctx, func(output any, err error) {
		for _, h := range hs {
			if err != nil {
				h.OnError(ctx, info, err)
			} else {
				h.OnEnd(ctx, info, output)
			}
		}
	}
}
