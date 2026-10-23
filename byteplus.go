package bungkus

import (
	"context"
	"net/http"
)

const bytePlusURL = "https://ark.ap-southeast.bytepluses.com/api/v3"

// BytePlus uses ModelArk. model is the model ID or endpoint ID.
type BytePlus struct {
	BaseURL string // default https://ark.ap-southeast.bytepluses.com/api/v3
	APIKey  string
	HTTP    *http.Client // nil uses http.DefaultClient
}

func (p *BytePlus) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	h := map[string]string{"Authorization": "Bearer " + p.APIKey}
	return chatCompletions(ctx, p.HTTP, "byteplus", baseOr(p.BaseURL, bytePlusURL), h, model, msgs)
}
