// Package logging logs model calls, tool calls and agent runs with log/slog.
//
//	ctx = bungkus.WithHandlers(ctx, logging.New(slog.Default()))
package logging

import (
	"context"
	"log/slog"
	"time"

	"github.com/jerichosiahaya/bungkus"
)

// New returns a handler that logs each run's start at Debug, its end at Info
// with the duration (and model, finish reason and tokens for model calls),
// and failures at Error.
func New(l *slog.Logger) bungkus.Handler { return handler{l} }

type handler struct{ l *slog.Logger }

type startKey struct{}

type start struct {
	at    time.Time
	model string
}

func (h handler) OnStart(ctx context.Context, info bungkus.RunInfo, input any) context.Context {
	s := start{at: time.Now()}
	if req, ok := input.(*bungkus.ChatRequest); ok {
		s.model = req.Model
	}
	h.l.DebugContext(ctx, "bungkus start", attrs(info, s)...)
	return context.WithValue(ctx, startKey{}, s)
}

func (h handler) OnEnd(ctx context.Context, info bungkus.RunInfo, output any) {
	s, _ := ctx.Value(startKey{}).(start)
	args := append(attrs(info, s), "duration", time.Since(s.at))
	if resp, ok := output.(*bungkus.ChatResponse); ok && resp != nil {
		args = append(args, "finish_reason", resp.FinishReason,
			"prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens)
	}
	h.l.InfoContext(ctx, "bungkus end", args...)
}

func (h handler) OnError(ctx context.Context, info bungkus.RunInfo, err error) {
	s, _ := ctx.Value(startKey{}).(start)
	h.l.ErrorContext(ctx, "bungkus error", append(attrs(info, s), "duration", time.Since(s.at), "error", err)...)
}

func attrs(info bungkus.RunInfo, s start) []any {
	args := []any{"component", info.Component, "name", info.Name}
	if s.model != "" {
		args = append(args, "model", s.model)
	}
	return args
}
