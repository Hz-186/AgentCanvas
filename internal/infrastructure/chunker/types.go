package chunker

import (
	"context"
	"fmt"
	"strings"

	"agentcanvas/internal/infrastructure/parser"
)

type Chunk struct {
	Index        int //
	Content      string
	TokenCount   int
	CharCount    int
	SectionTitle string
	PageNo       *int
	Metadata     map[string]any
}

type Policy struct {
	ChunkSize int
	Overlap   int
}

type Tokenizer interface {
	Name() string
	Count(text string) int
}

type Chunker interface {
	Method() string
	ChunkDocument(ctx context.Context, doc parser.ParsedDocument, policy Policy) ([]Chunk, error)
}

type Registry struct {
	items map[string]Chunker
}

func NewRegistry(items ...Chunker) *Registry {
	registry := &Registry{items: make(map[string]Chunker, len(items))}
	for _, item := range items {
		registry.Register(item)
	}
	return registry
}

func NewDefaultRegistry() *Registry {
	tokenizer := EstimatedTokenizer{}
	return NewRegistry(
		NewFixedTokenChunker(tokenizer),
		NewRecursiveChunker(tokenizer),
	)
}

func (r *Registry) Register(item Chunker) {
	if r == nil || item == nil {
		return
	}
	method := strings.TrimSpace(item.Method())
	if method == "" {
		return
	}
	r.items[method] = item
}

func (r *Registry) Select(method string) (Chunker, error) {
	if r == nil {
		return nil, fmt.Errorf("chunker registry is not configured")
	}
	method = strings.TrimSpace(method)
	if method == "" {
		method = MethodFixedToken
	}
	item, ok := r.items[method]
	if !ok {
		return nil, fmt.Errorf("unsupported chunk method: %s", method)
	}
	return item, nil
}

func (r *Registry) Chunk(ctx context.Context, method string, doc parser.ParsedDocument, policy Policy) ([]Chunk, error) {
	item, err := r.Select(method)
	if err != nil {
		return nil, err
	}
	return item.ChunkDocument(ctx, doc, policy)
}

const (
	MethodFixedToken = "fixed_token"
	MethodRecursive  = "recursive"
)
