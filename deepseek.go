package bungkus

import (
	"context"
	"iter"
)

var _ Provider = (*DeepSeek)(nil)

type DeepSeek struct{ client }

// NewDeepSeek returns a DeepSeek client using https://api.deepseek.com/v1.
func NewDeepSeek(apiKey string, opts ...Option) *DeepSeek {
	return &DeepSeek{newClient("deepseek", "https://api.deepseek.com/v1", map[string]string{"Authorization": "Bearer " + apiKey}, opts)}
}

func (p *DeepSeek) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.chatCompletions(ctx, req)
}

func (p *DeepSeek) ChatStream(ctx context.Context, req *ChatRequest) iter.Seq2[*ChatResponse, error] {
	return p.chatCompletionsStream(ctx, req)
}
