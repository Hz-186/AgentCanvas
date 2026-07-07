package parser

import (
	"context"
	"io"
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

func TestDefaultRegistryParsesPDFTextBlocks(t *testing.T) {
	p := NewDefaultRegistry()
	got, err := p.Parse(context.Background(), "manual.pdf", strings.NewReader("page one\fpage two"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.FileType != "pdf" || len(got.Blocks) != 2 {
		t.Fatalf("unexpected pdf parse result: %+v", got)
	}
	if got.Blocks[1].PageNo == nil || *got.Blocks[1].PageNo != 2 {
		t.Fatalf("expected second page number, got %+v", got.Blocks[1])
	}
	if got.Blocks[1].Metadata["page_no"] != 2 || got.Blocks[1].Metadata["parser_version"] != "deepdoc_pdf_text_v1" {
		t.Fatalf("expected pdf page metadata, got %+v", got.Blocks[1].Metadata)
	}
}

func TestDefaultRegistryParsesPDFLiteralTextThroughDeepdoc(t *testing.T) {
	p := NewDefaultRegistry()
	raw := `%PDF-1.4
1 0 obj
<< /Type /Page /Contents 2 0 R >>
endobj
2 0 obj
<< /Length 27 >>
stream
BT
(Enterprise RAG) Tj
ET
endstream
endobj`

	got, err := p.Parse(context.Background(), "manual.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.FileType != "pdf" || len(got.Blocks) != 1 {
		t.Fatalf("unexpected pdf parse result: %+v", got)
	}
	if got.Blocks[0].Text != "Enterprise RAG" {
		t.Fatalf("block text = %q, want Enterprise RAG", got.Blocks[0].Text)
	}
	if got.Blocks[0].Metadata["parser"] != "deepdoc_pdf_text" {
		t.Fatalf("expected deepdoc metadata, got %+v", got.Blocks[0].Metadata)
	}
}

func TestDefaultRegistryParsesPDFFAQBlocksThroughDeepdoc(t *testing.T) {
	p := NewDefaultRegistry()
	raw := "Q: What is BGE-M3?\nAliases: embedding\nA: A multilingual embedding model."

	got, err := p.Parse(context.Background(), "faq.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got.Blocks) != 1 || got.Blocks[0].Type != "faq" {
		t.Fatalf("unexpected blocks = %+v", got.Blocks)
	}
	if got.Blocks[0].Metadata["faq_question"] != "What is BGE-M3?" {
		t.Fatalf("faq metadata = %+v", got.Blocks[0].Metadata)
	}
}

func TestDefaultRegistrySplitsPDFLoosePageMarkers(t *testing.T) {
	p := NewDefaultRegistry()
	got, err := p.Parse(context.Background(), "manual.pdf", strings.NewReader("first page\nPage 2\nsecond page"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got.Blocks) != 3 {
		t.Fatalf("unexpected blocks = %+v", got.Blocks)
	}
	if got.Blocks[1].PageNo == nil || *got.Blocks[1].PageNo != 2 || got.Blocks[2].PageNo == nil || *got.Blocks[2].PageNo != 2 {
		t.Fatalf("expected second-page blocks, got %+v", got.Blocks)
	}
}

func TestTextParserBuildsFAQBlocks(t *testing.T) {
	p := NewTextParser()
	got, err := p.Parse(context.Background(), "faq.md", strings.NewReader("Q: What is AgentCanvas?\nAliases: AC, Agent Canvas\nCategory: runtime\nA: An agent runtime.\n\nQ: Why use RAG?\nA: Grounded answers."))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got.Blocks) != 2 || got.Blocks[0].Type != "faq" {
		t.Fatalf("expected faq blocks, got %+v", got.Blocks)
	}
	if got.Blocks[0].Metadata["chunk_hint"] != "single_faq" {
		t.Fatalf("expected faq metadata, got %+v", got.Blocks[0].Metadata)
	}
	aliases, ok := got.Blocks[0].Metadata["faq_aliases"].([]string)
	if !ok || len(aliases) != 2 || aliases[0] != "AC" || got.Blocks[0].Metadata["faq_category"] != "runtime" {
		t.Fatalf("expected faq aliases and category, got %+v", got.Blocks[0].Metadata)
	}
}

func TestOCRParserRequiresClient(t *testing.T) {
	p := NewOCRParser(nil)
	if _, err := p.Parse(context.Background(), "scan.png", strings.NewReader("fake")); err == nil {
		t.Fatal("Parse() error = nil, want missing ocr client error")
	}
}

func TestPDFParserUsesConfiguredOCRForScannedPDF(t *testing.T) {
	pageNo := 3
	p := NewPDFParser(fakeParserOCR{blocks: []DocumentBlock{{ID: "ocr1", Type: "ocr_text", Text: "scan text", PageNo: &pageNo}}})

	got, err := p.Parse(context.Background(), "scan.pdf", strings.NewReader("%PDF-1.4\n<< /Type /Page >>"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Text != "scan text" || len(got.Blocks) != 1 || got.Blocks[0].PageNo == nil || *got.Blocks[0].PageNo != 3 {
		t.Fatalf("unexpected OCR parse result = %+v", got)
	}
	if got.Blocks[0].Metadata["parser"] != "deepdoc_pdf_ocr" {
		t.Fatalf("expected deepdoc OCR metadata, got %+v", got.Blocks[0].Metadata)
	}
}

type fakeParserOCR struct {
	blocks []DocumentBlock
}

func (f fakeParserOCR) Recognize(context.Context, string, io.Reader) ([]DocumentBlock, error) {
	return f.blocks, nil
}
