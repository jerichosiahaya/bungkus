package bungkus

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestEmbed(t *testing.T) {
	// Out of order on purpose: results are placed by index.
	url, path, body := capture(t, `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`)
	for _, e := range []Embedder{NewOpenAI("k", WithBaseURL(url)), NewAzure(url, "k"), NewBytePlus("k", WithBaseURL(url))} {
		resp, err := e.Embed(context.Background(), &EmbedRequest{Model: "m", Input: []string{"a", "b"}, Dimensions: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resp.Embeddings, [][]float64{{1, 0}, {0, 1}}) || resp.Usage.PromptTokens != 4 {
			t.Errorf("%T: %+v", e, resp)
		}
		if *path != "/embeddings" || at(*body, "dimensions") != 2.0 || at(*body, "input", 1) != "b" || at(*body, "encoding_format") != "float" {
			t.Errorf("%T: %s %v", e, *path, *body)
		}
	}

	url, _, body = capture(t, `{"data":[]}`)
	NewOpenAI("k", WithBaseURL(url)).Embed(context.Background(), &EmbedRequest{})
	if _, ok := (*body)["dimensions"]; ok {
		t.Error("dimensions sent when 0")
	}
}

func TestEmbedErrors(t *testing.T) {
	var r recorder
	ctx := WithHandlers(context.Background(), &r)
	if _, err := NewOpenAI("k", WithBaseURL(serve(t, 200, `{"data":[{"index":5}]}`))).Embed(ctx, &EmbedRequest{Input: []string{"a"}}); err == nil || !strings.Contains(err.Error(), "index 5") {
		t.Errorf("bad index: %v", err)
	}
	if _, err := NewOpenAI("k", WithBaseURL(serve(t, 401, "no"))).Embed(ctx, &EmbedRequest{}); err == nil {
		t.Error("want an API error")
	}
	if got := strings.Join(r.events, "|"); got != "0 start embedder/openai *bungkus.EmbedRequest|error embedder/openai|0 start embedder/openai *bungkus.EmbedRequest|error embedder/openai" {
		t.Errorf("events: %s", got)
	}
}
