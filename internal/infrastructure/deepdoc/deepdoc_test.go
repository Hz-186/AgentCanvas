package deepdoc

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestExtractPDFTextReadsLiteralTextFromPDFBytes(t *testing.T) {
	raw := `%PDF-1.4
1 0 obj
<< /Type /Page /Contents 2 0 R >>
endobj
2 0 obj
<< /Length 44 >>
stream
BT
(AgentCanvas PDF) Tj
(Page text) Tj
ET
endstream
endobj`

	got, err := ExtractPDFText(context.Background(), "manual.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ExtractPDFText() error = %v", err)
	}
	if len(got.Pages) != 1 || len(got.Pages[0].Blocks) != 1 {
		t.Fatalf("unexpected pages = %+v", got.Pages)
	}
	if !strings.Contains(got.Text, "AgentCanvas PDF") || !strings.Contains(got.Text, "Page text") {
		t.Fatalf("extracted text = %q", got.Text)
	}
	if got.Pages[0].Blocks[0].Metadata["parser_version"] != "deepdoc_pdf_text_v1" {
		t.Fatalf("metadata = %+v", got.Pages[0].Blocks[0].Metadata)
	}
}

func TestExtractPDFTextReadsHexAndUTF16TextFromPDFBytes(t *testing.T) {
	raw := `%PDF-1.4
BT
<48656c6c6f20486578> Tj
<FEFF4E2D6587> Tj
ET`

	got, err := ExtractPDFText(context.Background(), "hex.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ExtractPDFText() error = %v", err)
	}
	if !strings.Contains(got.Text, "Hello Hex") || !strings.Contains(got.Text, "中文") {
		t.Fatalf("extracted text = %q", got.Text)
	}
}

func TestExtractPDFTextDecodesLiteralEscapes(t *testing.T) {
	raw := `%PDF-1.4
BT
(Agent\040Canvas \(PDF\)) Tj
ET`

	got, err := ExtractPDFText(context.Background(), "escape.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ExtractPDFText() error = %v", err)
	}
	if !strings.Contains(got.Text, "Agent Canvas (PDF)") {
		t.Fatalf("extracted text = %q", got.Text)
	}
}

func TestExtractPDFTextReturnsOCRHintForScannedPDF(t *testing.T) {
	_, err := ExtractPDFText(context.Background(), "scan.pdf", strings.NewReader("%PDF-1.4\n<< /Type /Page >>"))
	if err == nil {
		t.Fatal("ExtractPDFText() error = nil, want OCR hint")
	}
	if !strings.Contains(err.Error(), "configure OCR") {
		t.Fatalf("error = %q, want OCR hint", err.Error())
	}
}

func TestExtractPDFTextReturnsOCRHintForGarbledCIDPDF(t *testing.T) {
	raw := `%PDF-1.4
BT
((cid:123)(cid:456)(cid:789)) Tj
ET`
	_, err := ExtractPDFText(context.Background(), "garbled.pdf", strings.NewReader(raw))
	if err == nil {
		t.Fatal("ExtractPDFText() error = nil, want OCR hint")
	}
	if !strings.Contains(err.Error(), "configure OCR") {
		t.Fatalf("error = %q, want OCR hint", err.Error())
	}
}

func TestExtractPDFTextBuildsStructuredBlocks(t *testing.T) {
	raw := "# Architecture\n\nMilvus stores vectors.\n| col | val |\n- bullet"

	got, err := ExtractPDFText(context.Background(), "manual.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ExtractPDFText() error = %v", err)
	}
	if len(got.Pages) != 1 || len(got.Pages[0].Blocks) != 4 {
		t.Fatalf("unexpected blocks = %+v", got.Pages)
	}
	if got.Pages[0].Blocks[0].Type != "heading" || got.Pages[0].Blocks[0].Metadata["section_title"] != "Architecture" {
		t.Fatalf("heading metadata = %+v", got.Pages[0].Blocks[0])
	}
	if got.Pages[0].Blocks[2].Type != "table" || got.Pages[0].Blocks[3].Type != "list" {
		t.Fatalf("expected table and list blocks, got %+v", got.Pages[0].Blocks)
	}
}

func TestExtractPDFTextMergesStructuredLinesAndClassifiesCaptions(t *testing.T) {
	raw := "Table 1: Scores\n| name | score |\n| agent | 98 |\n- first\n- second\nPage 3\n正文段落。"

	got, err := ExtractPDFText(context.Background(), "manual.pdf", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ExtractPDFText() error = %v", err)
	}
	blocks := got.Pages[0].Blocks
	if len(blocks) != 5 {
		t.Fatalf("unexpected blocks = %+v", blocks)
	}
	if blocks[0].Type != "caption" || blocks[0].Metadata["caption"] == "" {
		t.Fatalf("caption block = %+v", blocks[0])
	}
	if blocks[1].Type != "table" || !strings.Contains(blocks[1].Text, "agent") {
		t.Fatalf("table block = %+v", blocks[1])
	}
	if blocks[2].Type != "list" || !strings.Contains(blocks[2].Text, "second") {
		t.Fatalf("list block = %+v", blocks[2])
	}
	if blocks[3].Type != "scrap" || blocks[4].Type != "text" {
		t.Fatalf("expected scrap then text, got %+v", blocks)
	}
}

func TestExtractPDFUsesOCRForScannedPDF(t *testing.T) {
	ocr := fakeDeepdocOCR{blocks: []Block{{Text: "recognized scan", PageNo: 2, BBox: &BBox{X: 1, Y: 2, Width: 3, Height: 4}}}}

	got, err := ExtractPDF(context.Background(), "scan.pdf", strings.NewReader("%PDF-1.4\n<< /Type /Page >>"), ExtractOptions{OCR: ocr})
	if err != nil {
		t.Fatalf("ExtractPDF() error = %v", err)
	}
	if got.Text != "recognized scan" || len(got.Pages) != 1 || got.Pages[0].PageNo != 2 {
		t.Fatalf("unexpected OCR result = %+v", got)
	}
	block := got.Pages[0].Blocks[0]
	if block.Metadata["parser"] != "deepdoc_pdf_ocr" || block.Metadata["bbox"] == nil {
		t.Fatalf("expected OCR metadata, got %+v", block.Metadata)
	}
}

type fakeDeepdocOCR struct {
	blocks []Block
}

func (f fakeDeepdocOCR) Recognize(context.Context, string, io.Reader) ([]Block, error) {
	return f.blocks, nil
}
