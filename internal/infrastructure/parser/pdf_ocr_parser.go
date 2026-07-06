package parser

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"agentcanvas/internal/infrastructure/deepdoc"
)

type PDFParser struct {
	OCR OCRClient
}

func NewPDFParser(clients ...OCRClient) *PDFParser {
	var client OCRClient
	if len(clients) > 0 {
		client = clients[0]
	}
	return &PDFParser{OCR: client}
}

func (p *PDFParser) Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), ".")) != "pdf" {
		return nil, fmt.Errorf("unsupported file type: %s", strings.TrimPrefix(filepath.Ext(filename), "."))
	}
	var opts deepdoc.ExtractOptions
	if p.OCR != nil {
		opts.OCR = pdfOCRAdapter{client: p.OCR}
	}
	extracted, err := deepdoc.ExtractPDF(ctx, filename, reader, opts)
	if err != nil {
		return nil, err
	}
	blocks := make([]DocumentBlock, 0)
	for _, page := range extracted.Pages {
		for _, block := range page.Blocks {
			pageNo := block.PageNo
			var bbox *BBox
			if block.BBox != nil {
				bbox = &BBox{X: block.BBox.X, Y: block.BBox.Y, Width: block.BBox.Width, Height: block.BBox.Height}
			}
			blocks = append(blocks, DocumentBlock{ID: block.ID, Type: block.Type, Text: block.Text, PageNo: &pageNo, BBox: bbox, Metadata: block.Metadata})
		}
	}
	return &ParsedDocument{Text: extracted.Text, FileType: "pdf", Blocks: blocks}, nil
}

type OCRClient interface {
	Recognize(ctx context.Context, filename string, reader io.Reader) ([]DocumentBlock, error)
}

type pdfOCRAdapter struct {
	client OCRClient
}

func (a pdfOCRAdapter) Recognize(ctx context.Context, filename string, reader io.Reader) ([]deepdoc.Block, error) {
	if a.client == nil {
		return nil, fmt.Errorf("ocr client is not configured")
	}
	blocks, err := a.client.Recognize(ctx, filename, reader)
	if err != nil {
		return nil, err
	}
	out := make([]deepdoc.Block, 0, len(blocks))
	for _, block := range blocks {
		pageNo := 1
		if block.PageNo != nil && *block.PageNo > 0 {
			pageNo = *block.PageNo
		}
		var bbox *deepdoc.BBox
		if block.BBox != nil {
			bbox = &deepdoc.BBox{X: block.BBox.X, Y: block.BBox.Y, Width: block.BBox.Width, Height: block.BBox.Height}
		}
		out = append(out, deepdoc.Block{ID: block.ID, Type: block.Type, Text: block.Text, PageNo: pageNo, BBox: bbox, Metadata: block.Metadata})
	}
	return out, nil
}

type OCRParser struct {
	Client OCRClient
}

func NewOCRParser(client OCRClient) *OCRParser { return &OCRParser{Client: client} }

func (p *OCRParser) Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error) {
	if p.Client == nil {
		return nil, fmt.Errorf("ocr client is not configured")
	}
	blocks, err := p.Client.Recognize(ctx, filename, reader)
	if err != nil {
		return nil, err
	}
	parts := make([]string, 0, len(blocks))
	for i := range blocks {
		block := &blocks[i]
		if block.Metadata == nil {
			block.Metadata = map[string]any{}
		}
		block.Metadata["parser"] = "ocr"
		block.Metadata["parser_version"] = "ocr_v1"
		block.Metadata["block_type"] = block.Type
		if block.PageNo != nil {
			block.Metadata["page_no"] = *block.PageNo
		}
		if block.BBox != nil {
			block.Metadata["bbox"] = block.BBox
		}
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return &ParsedDocument{Text: strings.Join(parts, "\n"), FileType: normalizeFileType(strings.TrimPrefix(filepath.Ext(filename), ".")), Blocks: blocks}, nil
}
