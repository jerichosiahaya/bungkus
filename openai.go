package bungkus

import "context"

var _ Provider = (*OpenAI)(nil)

type OpenAI struct{ client }

// NewOpenAI returns an OpenAI client using https://api.openai.com/v1.
func NewOpenAI(apiKey string, opts ...Option) *OpenAI {
	c := newClient("openai", "https://api.openai.com/v1", map[string]string{"Authorization": "Bearer " + apiKey}, opts)
	c.maxTokensField = "max_completion_tokens" // max_tokens is rejected by reasoning models
	return &OpenAI{c}
}

func (p *OpenAI) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.chatCompletions(ctx, req)
}
