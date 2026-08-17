package pythonbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"agentcanvas/internal/infrastructure/parser"
)

const LangChainPDFParser = "python:langchain_pdf"

// PDFParser adapts the Python LangChain parser to the existing parser.Parser
// contract. It only handles PDF files; the registry keeps all other formats
// on their existing Go implementations.
type PDFParser struct {
	Client      *Client
	Method      string
	Fallback    parser.Parser
	FallbackOCR bool
	MaxBytes    int
}

func NewPDFParser(client *Client, fallback parser.Parser, fallbackOCR bool, maxBytes int) *PDFParser {
	if maxBytes <= 0 && client != nil {
		maxBytes = client.config.MaxSendBytes
	}
	return &PDFParser{Client: client, Method: LangChainPDFParser, Fallback: fallback, FallbackOCR: fallbackOCR, MaxBytes: maxBytes}
}

func (p *PDFParser) Parse(ctx context.Context, filename string, reader io.Reader) (*parser.ParsedDocument, error) {
	if p == nil || p.Client == nil {
		return nil, fmt.Errorf("Python PDF parser is not configured")
	}
	if strings.ToLower(filepath.Ext(filename)) != ".pdf" {
		return nil, fmt.Errorf("unsupported file type: %s", strings.TrimPrefix(filepath.Ext(filename), "."))
	}
	content, err := readBounded(reader, p.MaxBytes)
	if err != nil {
		return nil, err
	}
	doc, requiresOCR, _, err := p.Client.ParseDocument(ctx, p.Method, filepath.Base(filename), content)
	if err != nil {
		return nil, err
	}
	if !requiresOCR {
		return doc, nil
	}
	if !p.FallbackOCR || p.Fallback == nil {
		return nil, fmt.Errorf("Python PDF parser requires OCR for %s", filename)
	}
	return p.Fallback.Parse(ctx, filename, bytes.NewReader(content))
}

// ShadowParser keeps the primary Go parser authoritative while comparing a
// candidate parser. It deliberately logs only aggregate metrics.
type ShadowParser struct {
	Primary  parser.Parser
	Shadow   parser.Parser
	Logger   *slog.Logger
	MaxBytes int
}

func NewShadowParser(primary, shadow parser.Parser, logger *slog.Logger, maxBytes int) *ShadowParser {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShadowParser{Primary: primary, Shadow: shadow, Logger: logger, MaxBytes: maxBytes}
}

func (s *ShadowParser) Parse(ctx context.Context, filename string, reader io.Reader) (*parser.ParsedDocument, error) {
	if s == nil || s.Primary == nil {
		return nil, fmt.Errorf("shadow parser primary is not configured")
	}
	content, err := readBounded(reader, s.MaxBytes)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	primary, err := s.Primary.Parse(ctx, filename, bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	if primary == nil {
		return nil, fmt.Errorf("primary parser returned an empty document")
	}
	if s.Shadow == nil {
		return primary, nil
	}
	shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	shadowStarted := time.Now()
	shadow, shadowErr := s.Shadow.Parse(shadowCtx, filename, bytes.NewReader(content))
	digest := sha256.Sum256(content)
	primaryMetrics := documentMetrics(primary)
	attrs := []any{
		"filename_ext", strings.ToLower(filepath.Ext(filename)),
		"content_sha256", fmt.Sprintf("%x", digest[:]),
		"primary_blocks", primaryMetrics.blocks,
		"primary_chars", primaryMetrics.chars,
		"primary_pages", primaryMetrics.pages,
		"primary_metadata_blocks", primaryMetrics.metadataBlocks,
		"primary_page_metadata_coverage", primaryMetrics.pageMetadataCoverage,
		"primary_duration_ms", time.Since(started).Milliseconds(),
		"shadow_duration_ms", time.Since(shadowStarted).Milliseconds(),
	}
	if shadowErr != nil {
		s.Logger.Warn("python bridge shadow parsing failed", append(attrs, "error", shadowErr.Error())...)
		return primary, nil
	}
	if shadow == nil {
		s.Logger.Warn("python bridge shadow parser returned an empty document", attrs...)
		return primary, nil
	}
	shadowMetrics := documentMetrics(shadow)
	attrs = append(attrs,
		"shadow_blocks", shadowMetrics.blocks,
		"shadow_chars", shadowMetrics.chars,
		"shadow_pages", shadowMetrics.pages,
		"shadow_metadata_blocks", shadowMetrics.metadataBlocks,
		"shadow_page_metadata_coverage", shadowMetrics.pageMetadataCoverage,
		"char_coverage", coverage(primaryMetrics.chars, shadowMetrics.chars),
	)
	s.Logger.Info("python bridge shadow parsing comparison", attrs...)
	return primary, nil
}

type documentMetricValues struct {
	blocks               int
	chars                int
	pages                int
	metadataBlocks       int
	pageMetadataCoverage float64
}

func documentMetrics(doc *parser.ParsedDocument) documentMetricValues {
	if doc == nil {
		return documentMetricValues{}
	}
	pages := make(map[int]struct{})
	pageBlocks := 0
	metadataBlocks := 0
	for _, block := range doc.Blocks {
		if block.PageNo != nil {
			pages[*block.PageNo] = struct{}{}
			pageBlocks++
		}
		if len(block.Metadata) > 0 {
			metadataBlocks++
		}
	}
	coverage := 1.0
	if len(doc.Blocks) > 0 {
		coverage = float64(pageBlocks) / float64(len(doc.Blocks))
	}
	return documentMetricValues{
		blocks:               len(doc.Blocks),
		chars:                len([]rune(doc.Text)),
		pages:                len(pages),
		metadataBlocks:       metadataBlocks,
		pageMetadataCoverage: coverage,
	}
}

func readBounded(reader io.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}
	content, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxBytes {
		return nil, fmt.Errorf("document exceeds Python parser input limit")
	}
	return content, nil
}

func coverage(primary, shadow int) float64 {
	if primary == 0 {
		if shadow == 0 {
			return 1
		}
		return 0
	}
	if shadow > primary {
		return float64(primary) / float64(shadow)
	}
	return float64(shadow) / float64(primary)
}
