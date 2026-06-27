package parser

import (
	"context"
	"strings"
	"testing"
)

func TestTextParserParsesTxtAndMarkdown(t *testing.T) {
	p := NewTextParser()

	cases := []struct {
		name     string
		filename string
		wantType string
	}{
		{name: "txt", filename: "note.txt", wantType: "txt"},
		{name: "md", filename: "README.md", wantType: "md"},
		{name: "markdown", filename: "guide.markdown", wantType: "md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.Parse(context.Background(), tc.filename, strings.NewReader("\ufeffhello"))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.FileType != tc.wantType {
				t.Fatalf("FileType = %q, want %q", got.FileType, tc.wantType)
			}
			if got.Text != "hello" {
				t.Fatalf("Text = %q, want hello", got.Text)
			}
		})
	}
}

func TestTextParserRejectsUnsupportedTypes(t *testing.T) {
	p := NewTextParser()

	if _, err := p.Parse(context.Background(), "report.pdf", strings.NewReader("hello")); err == nil {
		t.Fatal("Parse() error = nil, want unsupported type error")
	}
}

func TestTextParserBuildsBasicBlocks(t *testing.T) {
	p := NewTextParser()

	got, err := p.Parse(context.Background(), "guide.md", strings.NewReader("# Intro\n\nfirst paragraph\n\nsecond paragraph"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got.Blocks) != 3 {
		t.Fatalf("len(blocks) = %d, want 3", len(got.Blocks))
	}
	if got.Blocks[0].Type != "heading" || got.Blocks[0].Metadata["title"] != "Intro" {
		t.Fatalf("heading block = %#v", got.Blocks[0])
	}
	if got.Blocks[1].Type != "paragraph" || got.Blocks[1].Text != "first paragraph" {
		t.Fatalf("paragraph block = %#v", got.Blocks[1])
	}
}
