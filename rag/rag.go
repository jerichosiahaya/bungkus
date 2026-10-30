// Package rag splits documents, stores their embeddings, and retrieves the
// ones most similar to a query, for retrieval-augmented generation.
//
//	store := &rag.Memory{Embedder: bungkus.NewOpenAI(key), Model: "text-embedding-3-small"}
//	store.Add(ctx, rag.Chunks("handbook", text, 1000, 100)...)
//	docs, err := store.Retrieve(ctx, "how many leave days?", 4)
package rag

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/tool"
)

type Document struct {
	ID       string         `json:"id,omitempty"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Score    float64        `json:"score,omitempty"` // set by Retrieve: similarity to the query, higher is closer
}

// Retriever finds the k documents most relevant to a query. Memory implements
// it; implement it over your vector database for anything larger.
type Retriever interface {
	Retrieve(ctx context.Context, query string, k int) ([]Document, error)
}

var _ Retriever = (*Memory)(nil)

// Memory is an in-memory vector store ranked by cosine similarity. It is
// safe for concurrent use.
//
// ponytail: linear scan over every document; fine up to ~100k chunks, use a
// vector database (pgvector, Qdrant...) behind Retriever beyond that.
type Memory struct {
	Embedder bungkus.Embedder
	Model    string
	// BatchSize is how many documents Add embeds per request; 0 means 256.
	BatchSize int

	mu   sync.RWMutex
	docs []Document
	vecs [][]float64
}

// Add embeds the documents' Content and stores them.
func (m *Memory) Add(ctx context.Context, docs ...Document) error {
	batch := m.BatchSize
	if batch <= 0 {
		batch = 256
	}
	for b := range slices.Chunk(docs, batch) {
		texts := make([]string, len(b))
		for i, d := range b {
			texts[i] = d.Content
		}
		vecs, err := m.embed(ctx, texts)
		if err != nil {
			return err
		}
		m.mu.Lock()
		m.docs = append(m.docs, b...)
		m.vecs = append(m.vecs, vecs...)
		m.mu.Unlock()
	}
	return nil
}

// Len is the number of stored documents.
func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.docs)
}

// Retrieve returns up to k documents, most similar first, with Score set.
func (m *Memory) Retrieve(ctx context.Context, query string, k int) (docs []Document, err error) {
	ctx, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentRetriever, Name: "memory"}, query)
	defer func() { end(docs, err) }()

	vecs, err := m.embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	scored := make([]Document, len(m.docs))
	for i, d := range m.docs {
		d.Score = cosine(vecs[0], m.vecs[i])
		scored[i] = d
	}
	m.mu.RUnlock()
	slices.SortStableFunc(scored, func(a, b Document) int { return cmp.Compare(b.Score, a.Score) })
	return scored[:min(k, len(scored))], nil
}

func (m *Memory) embed(ctx context.Context, texts []string) ([][]float64, error) {
	resp, err := m.Embedder.Embed(ctx, &bungkus.EmbedRequest{Model: m.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("rag: got %d embeddings for %d texts", len(resp.Embeddings), len(texts))
	}
	return resp.Embeddings, nil
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range min(len(a), len(b)) {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// Split cuts text into chunks of at most size runes, each starting overlap
// runes before the previous one ended. It ends chunks at the last paragraph,
// line, sentence or word break in their second half, when there is one.
func Split(text string, size, overlap int) []string {
	r := []rune(text)
	var out []string
	for start := 0; start < len(r); {
		end := min(start+size, len(r))
		if end < len(r) {
			end = breakPoint(r, start+(end-start)/2, end)
		}
		if chunk := strings.TrimSpace(string(r[start:end])); chunk != "" {
			out = append(out, chunk)
		}
		if end == len(r) {
			break
		}
		start = max(end-overlap, start+1)
	}
	return out
}

// breakPoint returns the end of the last separator in r[lo:hi], trying
// coarser separators first, or hi if there is none.
func breakPoint(r []rune, lo, hi int) int {
	for _, sep := range [][]rune{[]rune("\n\n"), []rune("\n"), []rune(". "), []rune(" ")} {
		for i := hi - len(sep); i >= lo; i-- {
			if slices.Equal(r[i:i+len(sep)], sep) {
				return i + len(sep)
			}
		}
	}
	return hi
}

// Chunks splits text and returns the chunks as documents with IDs
// "<source>#0", "<source>#1"... and Metadata {"source": source}.
func Chunks(source, text string, size, overlap int) []Document {
	parts := Split(text, size, overlap)
	docs := make([]Document, len(parts))
	for i, p := range parts {
		docs[i] = Document{ID: fmt.Sprintf("%s#%d", source, i), Content: p, Metadata: map[string]any{"source": source}}
	}
	return docs
}

// NewTool returns a tool that lets an agent search r. The model sends a
// query and gets back the top k documents as JSON.
func NewTool(name, description string, r Retriever, k int) tool.Invokable {
	type args struct {
		Query string `json:"query" desc:"What to search for"`
	}
	return tool.New(name, description, func(ctx context.Context, a args) ([]Document, error) {
		return r.Retrieve(ctx, a.Query, k)
	})
}
