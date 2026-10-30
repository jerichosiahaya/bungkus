package bungkus

import (
	"context"
	"fmt"
)

var (
	_ Embedder = (*OpenAI)(nil)
	_ Embedder = (*Azure)(nil)
	_ Embedder = (*BytePlus)(nil)
)

type EmbedRequest struct {
	Model      string // for azure the deployment name
	Input      []string
	Dimensions int // 0 uses the model default; only some models can shorten
}

type EmbedResponse struct {
	Embeddings [][]float64 // one per Input, in order
	Usage      Usage
}

// Embedder turns text into vectors. OpenAI, Azure and BytePlus implement it;
// Anthropic and DeepSeek have no embeddings API.
type Embedder interface {
	Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error)
}

func (p *OpenAI) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	return p.embed(ctx, req)
}

func (p *Azure) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	return p.embed(ctx, req)
}

func (p *BytePlus) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	return p.embed(ctx, req)
}

// embed calls the OpenAI-style /embeddings endpoint.
func (c *client) embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	ctx, end := StartRun(ctx, RunInfo{Component: ComponentEmbedder, Name: c.name}, req)
	resp, err := c.embedCall(ctx, req)
	end(resp, err)
	return resp, err
}

func (c *client) embedCall(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	body := map[string]any{"model": req.Model, "input": req.Input, "encoding_format": "float"}
	if req.Dimensions > 0 {
		body["dimensions"] = req.Dimensions
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage Usage `json:"usage"`
	}
	if err := c.post(ctx, "/embeddings", body, &out); err != nil {
		return nil, err
	}
	resp := &EmbedResponse{Embeddings: make([][]float64, len(req.Input)), Usage: out.Usage}
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(req.Input) {
			return nil, fmt.Errorf("bungkus: %s returned embedding index %d for %d inputs", c.name, d.Index, len(req.Input))
		}
		resp.Embeddings[d.Index] = d.Embedding
	}
	return resp, nil
}
