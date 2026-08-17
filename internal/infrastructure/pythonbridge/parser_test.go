package pythonbridge

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"agentcanvas/internal/infrastructure/parser"
	bridgegen "agentcanvas/internal/infrastructure/pythonbridge/gen"
)

type recordingDocumentParser struct {
	doc   *parser.ParsedDocument
	err   error
	calls int
}

func (p *recordingDocumentParser) Parse(_ context.Context, _ string, reader io.Reader) (*parser.ParsedDocument, error) {
	p.calls++
	_, _ = io.ReadAll(reader)
	if p.err != nil {
		return nil, p.err
	}
	return p.doc, nil
}

func TestPDFParserUsesPythonDocumentAndEnforcesInputLimit(t *testing.T) {
	page := 2
	fallback := &recordingDocumentParser{doc: &parser.ParsedDocument{Text: "go"}}
	fake := &fakeBridgeClient{parseResponse: &bridgegen.ParseDocumentResponse{
		Parser: "python:langchain_pdf", ImplementationVersion: "langchain-pymupdf-v1",
		Document: &bridgegen.ParsedDocument{FileType: "pdf", Text: "python", Blocks: []*bridgegen.DocumentBlock{{
			Id: "p2_b1", Type: "text", Text: "python", PageNo: func() *int32 { value := int32(page); return &value }(),
		}}},
	}}
	client := &Client{stub: fake, config: Config{MaxSendBytes: 32}}
	pdfParser := NewPDFParser(client, fallback, true, 4)

	doc, err := pdfParser.Parse(context.Background(), "/private/documents/guide.pdf", strings.NewReader("pdf"))
	if err != nil || doc == nil || doc.Text != "python" || fallback.calls != 0 {
		t.Fatalf("Parse() = doc=%+v err=%v fallback_calls=%d", doc, err, fallback.calls)
	}
	if fake.parseRequest.GetFilename() != "guide.pdf" {
		t.Fatalf("Python Bridge received unsafe filename %q", fake.parseRequest.GetFilename())
	}
	if _, err := pdfParser.Parse(context.Background(), "guide.pdf", strings.NewReader("oversized")); err == nil {
		t.Fatal("PDFParser accepted a document over its configured byte limit")
	}
	if _, err := pdfParser.Parse(context.Background(), "guide.txt", strings.NewReader("txt")); err == nil {
		t.Fatal("PDFParser accepted a non-PDF filename")
	}
}

func TestPDFParserFallsBackToGoOCROnlyWhenPythonRequiresIt(t *testing.T) {
	fallback := &recordingDocumentParser{doc: &parser.ParsedDocument{Text: "ocr"}}
	fake := &fakeBridgeClient{parseResponse: &bridgegen.ParseDocumentResponse{
		Parser: "python:langchain_pdf", ImplementationVersion: "langchain-pymupdf-v1", RequiresOcr: true,
	}}
	client := &Client{stub: fake, config: Config{MaxSendBytes: 32}}
	doc, err := NewPDFParser(client, fallback, true, 32).Parse(context.Background(), "scan.pdf", strings.NewReader("same input"))
	if err != nil || doc == nil || doc.Text != "ocr" || fallback.calls != 1 {
		t.Fatalf("OCR fallback = doc=%+v err=%v calls=%d", doc, err, fallback.calls)
	}
	if _, err := NewPDFParser(client, fallback, false, 32).Parse(context.Background(), "scan.pdf", strings.NewReader("same input")); err == nil {
		t.Fatal("PDFParser silently accepted requires_ocr without an OCR fallback")
	}
}

func TestShadowParserKeepsPrimaryAndDoesNotFailOnCandidateError(t *testing.T) {
	primary := &recordingDocumentParser{doc: &parser.ParsedDocument{Text: "go"}}
	shadow := &recordingDocumentParser{err: errors.New("bridge unavailable")}
	got, err := NewShadowParser(primary, shadow, nil, 32).Parse(context.Background(), "guide.pdf", strings.NewReader("pdf"))
	if err != nil || got == nil || got.Text != "go" || primary.calls != 1 || shadow.calls != 1 {
		t.Fatalf("shadow parse = doc=%+v err=%v primary=%d shadow=%d", got, err, primary.calls, shadow.calls)
	}
}
