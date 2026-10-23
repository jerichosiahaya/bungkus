package bungkus

import "context"

var _ Provider = (*BytePlus)(nil)

// BytePlus uses ModelArk. ChatRequest.Model is the model ID or endpoint ID.
type BytePlus struct{ client }

// NewBytePlus returns a ModelArk client using https://ark.ap-southeast.bytepluses.com/api/v3.
func NewBytePlus(apiKey string, opts ...Option) *BytePlus {
	return &BytePlus{newClient("byteplus", "https://ark.ap-southeast.bytepluses.com/api/v3", map[string]string{"Authorization": "Bearer " + apiKey}, opts)}
}

func (p *BytePlus) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.chatCompletions(ctx, req)
}
