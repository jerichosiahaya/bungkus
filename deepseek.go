package bungkus

import (
	"context"
	"net/http"
)

const deepSeekURL = "https://api.deepseek.com/v1"

type DeepSeek struct {
	BaseURL string // default https://api.deepseek.com/v1
	APIKey  string
	HTTP    *http.Client // nil uses http.DefaultClient
}

func (p *DeepSeek) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	h := map[string]string{"Authorization": "Bearer " + p.APIKey}
	return chatCompletions(ctx, p.HTTP, "deepseek", baseOr(p.BaseURL, deepSeekURL), h, model, msgs)
}
