package bungkus

import (
	"context"
	"net/http"
)

// Azure uses the Azure OpenAI v1 API. model is the deployment name.
type Azure struct {
	BaseURL string // required: https://<resource>.openai.azure.com/openai/v1
	APIKey  string
	HTTP    *http.Client // nil uses http.DefaultClient
}

func (p *Azure) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	h := map[string]string{"api-key": p.APIKey}
	return chatCompletions(ctx, p.HTTP, "azure", baseOr(p.BaseURL, ""), h, model, msgs)
}
