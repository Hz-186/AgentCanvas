package parser

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type Registry struct {
	items map[string]Parser
}

func NewDefaultRegistry() *Registry {
	return NewDefaultRegistryWithOCR(nil)
}

func NewDefaultRegistryWithOCR(ocr OCRClient) *Registry {
	return NewRegistry(map[string]Parser{
		"txt":      NewTextParser(),
		"md":       NewTextParser(),
		"markdown": NewTextParser(),
		"pdf":      NewPDFParser(ocr),
		"png":      NewOCRParser(ocr),
		"jpg":      NewOCRParser(ocr),
		"jpeg":     NewOCRParser(ocr),
	})
}

func NewRegistry(items map[string]Parser) *Registry {
	r := &Registry{items: map[string]Parser{}}
	for ext, item := range items {
		r.Register(ext, item)
	}
	return r
}

func (r *Registry) Register(ext string, parser Parser) {
	if r == nil || parser == nil {
		return
	}
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if ext == "" {
		return
	}
	r.items[ext] = parser
}

func (r *Registry) Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error) {
	if r == nil {
		return nil, fmt.Errorf("parser registry is not configured")
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	if ext == "" {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
	item := r.items[ext]
	if item == nil {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
	return item.Parse(ctx, filename, reader)
}
