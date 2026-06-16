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
