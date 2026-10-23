package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/jerichosiahaya/bungkus"
)

func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	h := New(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ctx := bungkus.WithHandlers(context.Background(), h)

	_, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentModel, Name: "openai"}, &bungkus.ChatRequest{Model: "gpt-5"})
	end(&bungkus.ChatResponse{FinishReason: "stop", Usage: bungkus.Usage{PromptTokens: 7, CompletionTokens: 3}}, nil)
	_, end = bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentTool, Name: "weather"}, bungkus.ToolCall{})
	end(nil, errors.New("api down"))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 log lines, got:\n%s", buf.String())
	}
	var rec []map[string]any
	for _, l := range lines {
		var m map[string]any
		json.Unmarshal([]byte(l), &m)
		rec = append(rec, m)
	}
	checks := []struct {
		line       int
		key, value any
	}{
		{0, "level", "DEBUG"}, {0, "model", "gpt-5"},
		{1, "msg", "bungkus end"}, {1, "model", "gpt-5"}, {1, "finish_reason", "stop"}, {1, "prompt_tokens", 7.0}, {1, "completion_tokens", 3.0},
		{2, "component", "tool"}, {2, "name", "weather"},
		{3, "level", "ERROR"}, {3, "error", "api down"},
	}
	for _, c := range checks {
		if got := rec[c.line][c.key.(string)]; got != c.value {
			t.Errorf("line %d %s: got %v, want %v", c.line, c.key, got, c.value)
		}
	}
	if _, ok := rec[1]["duration"]; !ok {
		t.Error("end line has no duration")
	}
	if _, ok := rec[3]["model"]; ok {
		t.Error("tool lines should not have a model")
	}
}
