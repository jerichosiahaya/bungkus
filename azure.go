package bungkus

import "context"

var _ Provider = (*Azure)(nil)

// Azure uses the Azure OpenAI v1 API. ChatRequest.Model is the deployment name.
type Azure struct{ client }

// NewAzure takes the resource endpoint, e.g. https://<resource>.openai.azure.com/openai/v1.
func NewAzure(endpoint, apiKey string, opts ...Option) *Azure {
	opts = append([]Option{WithBaseURL(endpoint)}, opts...)
	c := newClient("azure", "", map[string]string{"api-key": apiKey}, opts)
	c.maxTokensField = "max_completion_tokens"
	return &Azure{c}
}

func (p *Azure) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.chatCompletions(ctx, req)
}
