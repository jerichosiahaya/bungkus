package bungkus

import (
	"context"
	"net/http"
)

const openAIURL = "https://api.openai.com/v1"

type OpenAI struct {
	BaseURL string // default https://api.openai.com/v1
	APIKey  string
	HTTP    *http.Client // nil uses http.DefaultClient
}

func (p *OpenAI) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	h := map[string]string{"Authorization": "Bearer " + p.APIKey}
	return chatCompletions(ctx, p.HTTP, "openai", baseOr(p.BaseURL, openAIURL), h, model, msgs)
}
