package bungkus_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/agent"
	"github.com/jerichosiahaya/bungkus/tool"
)

// Live tests call the real APIs. They run only with BUNGKUS_LIVE=1, for each
// provider whose key is set:
//
//	BUNGKUS_LIVE=1 DEEPSEEK_API_KEY=... go test -run Live -v
//
// Override a model with BUNGKUS_<PROVIDER>_MODEL, e.g. BUNGKUS_OPENAI_MODEL.
// Keys and models can also go in a .env file next to this one (git-ignored);
// its non-empty values override the environment.
type live struct {
	name, model    string
	provider       bungkus.Provider
	images, files  bool
	schemaRequired bool // false: the provider only supports a JSON object without a schema
}

func liveProviders(t *testing.T) []live {
	if os.Getenv("BUNGKUS_LIVE") != "1" {
		t.Skip("set BUNGKUS_LIVE=1 to call the real APIs")
	}
	loadDotEnv(t)
	model := func(name, def string) string {
		if m := os.Getenv("BUNGKUS_" + strings.ToUpper(name) + "_MODEL"); m != "" {
			return m
		}
		return def
	}
	var ps []live
	if k := os.Getenv("OPENAI_API_KEY"); k != "" {
		ps = append(ps, live{"openai", model("openai", "gpt-5-mini"), bungkus.NewOpenAI(k), true, true, true})
	}
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		ps = append(ps, live{"anthropic", model("anthropic", "claude-haiku-5-5"), bungkus.NewAnthropic(k), true, true, true})
	}
	if k, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_ENDPOINT"); k != "" && ep != "" {
		ps = append(ps, live{"azure", model("azure", os.Getenv("AZURE_OPENAI_DEPLOYMENT")), bungkus.NewAzure(ep, k), true, true, true})
	}
	if k := os.Getenv("BYTEPLUS_API_KEY"); k != "" {
		ps = append(ps, live{"byteplus", model("byteplus", ""), bungkus.NewBytePlus(k), true, false, true})
	}
	if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
		ps = append(ps, live{"deepseek", model("deepseek", "deepseek-chat"), bungkus.NewDeepSeek(k), false, false, false})
	}
	if len(ps) == 0 {
		t.Skip("no provider API keys set")
	}
	return ps
}

// loadDotEnv sets the KEY=value lines of .env, if there is one, for this test.
func loadDotEnv(t *testing.T) {
	b, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(line, "=")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if !ok || v == "" || strings.HasPrefix(line, "#") { // an empty value does not hide the environment's
			continue
		}
		t.Setenv(strings.TrimSpace(k), v)
	}
}

func eachLive(t *testing.T, f func(t *testing.T, l live, ctx context.Context)) {
	for _, l := range liveProviders(t) {
		t.Run(l.name, func(t *testing.T) {
			if l.model == "" {
				t.Skipf("set BUNGKUS_%s_MODEL", strings.ToUpper(l.name))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			f(t, l, ctx)
		})
	}
}

func (l live) req(content string, parts ...bungkus.Part) *bungkus.ChatRequest {
	return &bungkus.ChatRequest{Model: l.model, MaxTokens: 2000, Messages: []bungkus.Message{{Role: "user", Content: content, Parts: parts}}}
}

func TestLiveChat(t *testing.T) {
	eachLive(t, func(t *testing.T, l live, ctx context.Context) {
		resp, err := l.provider.Chat(ctx, l.req("Reply with the single word: pong"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(resp.Content), "pong") || resp.FinishReason != bungkus.FinishStop || resp.Usage.PromptTokens == 0 {
			t.Errorf("got %q, finish %q, usage %+v", resp.Content, resp.FinishReason, resp.Usage)
		}
	})
}

func TestLiveStream(t *testing.T) {
	eachLive(t, func(t *testing.T, l live, ctx context.Context) {
		chunks := 0
		resp, err := bungkus.Collect(func(yield func(*bungkus.ChatResponse, error) bool) {
			for c, err := range l.provider.ChatStream(ctx, l.req("Count from 1 to 20, separated by spaces.")) {
				chunks++
				if !yield(c, err) {
					return
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if chunks < 3 || !strings.Contains(resp.Content, "20") || resp.FinishReason != bungkus.FinishStop || resp.Usage.CompletionTokens == 0 {
			t.Errorf("%d chunks, got %q, finish %q, usage %+v", chunks, resp.Content, resp.FinishReason, resp.Usage)
		}
	})
}

// TestLiveAgent runs a tool loop, which fails if reasoning is not sent back
// correctly (DeepSeek returns 400, Claude rejects modified thinking blocks).
func TestLiveAgent(t *testing.T) {
	type args struct {
		City string `json:"city"`
	}
	weather := tool.New("get_weather", "Current weather for a city", func(_ context.Context, a args) (string, error) {
		return "31°C and sunny in " + a.City, nil
	})
	eachLive(t, func(t *testing.T, l live, ctx context.Context) {
		a := &agent.ReAct{Provider: l.provider, Request: bungkus.ChatRequest{Model: l.model, MaxTokens: 2000}, Tools: []tool.Invokable{weather}}
		history := []bungkus.Message{{Role: "user", Content: "What is the weather in Denpasar? Use the tool."}}
		check := func(how string, res *agent.Result, err error) {
			if err != nil {
				t.Fatalf("%s: %v", how, err)
			}
			if len(res.Messages) < 3 || !strings.Contains(res.Answer(), "31") {
				t.Errorf("%s: %d messages, answer %q", how, len(res.Messages), res.Answer())
			}
		}
		res, err := a.Run(ctx, history)
		check("run", res, err)

		var streamed *agent.Result
		for e, err := range a.Stream(ctx, history) {
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			if e.Result != nil {
				streamed = e.Result
			}
		}
		check("stream", streamed, nil)
	})
}

func TestLiveJSON(t *testing.T) {
	eachLive(t, func(t *testing.T, l live, ctx context.Context) {
		req := l.req("Give the capital of Indonesia as JSON with the key city.")
		req.ResponseFormat = &bungkus.ResponseFormat{}
		if l.schemaRequired {
			req.ResponseFormat = &bungkus.ResponseFormat{Name: "capital", Schema: json.RawMessage(
				`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`)}
		}
		resp, err := l.provider.Chat(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		var out struct{ City string }
		if err := json.Unmarshal([]byte(resp.Content), &out); err != nil || !strings.Contains(out.City, "Jakarta") {
			t.Errorf("got %q (%v)", resp.Content, err)
		}
	})
}

func TestLiveImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = []uint8{255, 0, 0, 255}[i%4]
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	eachLive(t, func(t *testing.T, l live, ctx context.Context) {
		if !l.images {
			t.Skip("no image input")
		}
		resp, err := l.provider.Chat(ctx, l.req("What color is this image? One word.", bungkus.Part{Type: bungkus.PartImage, URL: bungkus.DataURL("image/png", buf.Bytes())}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(resp.Content), "red") {
			t.Errorf("got %q", resp.Content)
		}
	})
}

func TestLiveFile(t *testing.T) {
	pdf := minimalPDF("The secret word is bungkus.")
	eachLive(t, func(t *testing.T, l live, ctx context.Context) {
		if !l.files {
			t.Skip("no file input")
		}
		resp, err := l.provider.Chat(ctx, l.req("What is the secret word in this document? One word.", bungkus.Part{Type: bungkus.PartFile, URL: bungkus.DataURL("application/pdf", pdf), Name: "secret.pdf"}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(resp.Content), "bungkus") {
			t.Errorf("got %q", resp.Content)
		}
	})
}

// minimalPDF returns a one-page PDF showing text.
func minimalPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}
