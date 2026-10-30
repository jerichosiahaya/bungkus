package rag

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jerichosiahaya/bungkus"
)

// fakeEmbedder embeds text as counts of the words cat, dog and fish.
type fakeEmbedder struct {
	calls [][]string
	err   error
	short bool // return one embedding too few
}

func (f *fakeEmbedder) Embed(_ context.Context, req *bungkus.EmbedRequest) (*bungkus.EmbedResponse, error) {
	f.calls = append(f.calls, req.Input)
	if f.err != nil {
		return nil, f.err
	}
	resp := &bungkus.EmbedResponse{}
	for _, s := range req.Input {
		resp.Embeddings = append(resp.Embeddings, []float64{
			float64(strings.Count(s, "cat")), float64(strings.Count(s, "dog")), float64(strings.Count(s, "fish")),
		})
	}
	if f.short {
		resp.Embeddings = resp.Embeddings[1:]
	}
	return resp, nil
}

func TestMemory(t *testing.T) {
	e := &fakeEmbedder{}
	m := &Memory{Embedder: e, Model: "m", BatchSize: 2}
	ctx := context.Background()
	err := m.Add(ctx,
		Document{ID: "1", Content: "cat cat"},
		Document{ID: "2", Content: "dog"},
		Document{ID: "3", Content: "cat dog"},
		Document{ID: "4", Content: "nothing"}, // zero vector
	)
	if err != nil || m.Len() != 4 || len(e.calls) != 2 {
		t.Fatalf("add: %v, %d docs, %d calls", err, m.Len(), len(e.calls))
	}

	docs, err := m.Retrieve(ctx, "cat", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].ID != "1" || docs[1].ID != "3" || docs[0].Score != 1 || docs[1].Score < 0.7 || docs[1].Score > 0.71 {
		t.Errorf("retrieve: %+v", docs)
	}
	if docs, _ := m.Retrieve(ctx, "fish", 10); len(docs) != 4 || docs[3].Score != 0 {
		t.Errorf("k beyond len: %+v", docs)
	}
}

func TestMemoryErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	if err := (&Memory{Embedder: &fakeEmbedder{err: boom}}).Add(ctx, Document{}); !errors.Is(err, boom) {
		t.Errorf("add: %v", err)
	}
	if err := (&Memory{Embedder: &fakeEmbedder{short: true}}).Add(ctx, Document{}); err == nil || !strings.Contains(err.Error(), "0 embeddings for 1") {
		t.Errorf("short: %v", err)
	}

	var rec recorder
	ctx = bungkus.WithHandlers(ctx, &rec)
	if _, err := (&Memory{Embedder: &fakeEmbedder{err: boom}}).Retrieve(ctx, "q", 1); !errors.Is(err, boom) {
		t.Errorf("retrieve: %v", err)
	}
	(&Memory{Embedder: &fakeEmbedder{}}).Retrieve(ctx, "q", 1)
	if want := "start retriever/memory q|error boom|start retriever/memory q|end 0 docs"; strings.Join(rec, "|") != want {
		t.Errorf("callbacks: %v", rec)
	}
}

type recorder []string

func (r *recorder) OnStart(ctx context.Context, info bungkus.RunInfo, input any) context.Context {
	*r = append(*r, "start "+info.Component+"/"+info.Name+" "+input.(string))
	return ctx
}
func (r *recorder) OnEnd(_ context.Context, _ bungkus.RunInfo, output any) {
	*r = append(*r, "end "+string(rune('0'+len(output.([]Document))))+" docs")
}
func (r *recorder) OnError(_ context.Context, _ bungkus.RunInfo, err error) {
	*r = append(*r, "error "+err.Error())
}

func TestSplit(t *testing.T) {
	for _, c := range []struct {
		text          string
		size, overlap int
		want          []string
	}{
		{"", 10, 0, nil},
		{"short", 10, 0, []string{"short"}},
		{"para one.\n\npara two is here", 20, 0, []string{"para one.", "para two is here"}},
		{"one two three four", 10, 0, []string{"one two", "three four"}},
		{"aaaaaaaaaaaa", 5, 0, []string{"aaaaa", "aaaaa", "aa"}}, // no break: cut at size
		{"one two three four", 10, 4, []string{"one two", "two three", "ree four"}},
		{"héllo wörld ünïcode", 12, 0, []string{"héllo wörld", "ünïcode"}},
		{"ab", 1, 5, []string{"a", "b"}}, // overlap >= size still moves forward
		{"   \n\n   ", 3, 0, nil},
	} {
		if got := Split(c.text, c.size, c.overlap); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q, %d, %d) = %q, want %q", c.text, c.size, c.overlap, got, c.want)
		}
	}
}

func TestChunks(t *testing.T) {
	docs := Chunks("guide.md", "one two three four", 10, 0)
	want := []Document{
		{ID: "guide.md#0", Content: "one two", Metadata: map[string]any{"source": "guide.md"}},
		{ID: "guide.md#1", Content: "three four", Metadata: map[string]any{"source": "guide.md"}},
	}
	if !reflect.DeepEqual(docs, want) {
		t.Errorf("got %+v", docs)
	}
}

func TestNewTool(t *testing.T) {
	m := &Memory{Embedder: &fakeEmbedder{}}
	m.Add(context.Background(), Document{ID: "a", Content: "dog"})
	tl := NewTool("search_docs", "Search the handbook", m, 3)
	if info := tl.Info(); info.Name != "search_docs" || !strings.Contains(string(info.Parameters), `"query"`) {
		t.Errorf("info: %+v", info)
	}
	out, err := tl.Invoke(context.Background(), `{"query":"dog"}`)
	if err != nil || out != `[{"id":"a","content":"dog","score":1}]` {
		t.Errorf("got %s, %v", out, err)
	}
}
