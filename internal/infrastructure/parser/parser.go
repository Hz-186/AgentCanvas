package parser

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type ParsedDocument struct {
	Text     string
	FileType string
}

type Parser interface {
	Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error)
}

type TextParser struct{}

func NewTextParser() *TextParser {
	return &TextParser{}
}

func (p *TextParser) Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	switch ext {
	case "txt", "md", "markdown":
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	return &ParsedDocument{Text: text, FileType: normalizeFileType(ext)}, nil
}

func normalizeFileType(ext string) string {
	if ext == "markdown" {
		return "md"
	}
	return ext
}
