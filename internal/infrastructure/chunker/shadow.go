package chunker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"agentcanvas/internal/infrastructure/parser"
)

// ShadowChunker runs a candidate chunker for comparison while keeping the
// primary result authoritative. Shadow failures never change ingestion status.
type ShadowChunker struct {
	Primary Chunker
	Shadow  Chunker
	Logger  *slog.Logger
}

func NewShadowChunker(primary, shadow Chunker, logger *slog.Logger) *ShadowChunker {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShadowChunker{Primary: primary, Shadow: shadow, Logger: logger}
}

func (s *ShadowChunker) Method() string {
	if s == nil || s.Primary == nil {
		return ""
	}
	return s.Primary.Method()
}

func (s *ShadowChunker) ChunkDocument(ctx context.Context, doc parser.ParsedDocument, policy Policy) ([]Chunk, error) {
	if s == nil || s.Primary == nil {
		return nil, fmt.Errorf("shadow chunker primary is not configured")
	}
	primaryStarted := time.Now()
	primary, err := s.Primary.ChunkDocument(ctx, doc, policy)
	if err != nil {
		return nil, err
	}
	primaryDuration := time.Since(primaryStarted)
	if s.Shadow == nil {
		return primary, nil
	}
	shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	shadowStarted := time.Now()
	shadow, shadowErr := s.Shadow.ChunkDocument(shadowCtx, doc, policy)
	digest := sha256.Sum256([]byte(doc.Text))
	attrs := []any{
		"method", s.Method(),
		"shadow_method", s.Shadow.Method(),
		"content_sha256", fmt.Sprintf("%x", digest[:]),
		"primary_chunks", len(primary),
		"shadow_duration_ms", time.Since(shadowStarted).Milliseconds(),
		"primary_duration_ms", primaryDuration.Milliseconds(),
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if shadowErr != nil {
		logger.Warn("python bridge shadow chunking failed", append(attrs, "error", shadowErr.Error())...)
		return primary, nil
	}
	metrics := compareChunks(primary, shadow)
	for key, value := range metrics {
		attrs = append(attrs, key, value)
	}
	logger.Info("python bridge shadow chunking comparison", attrs...)
	return primary, nil
}

func compareChunks(primary, shadow []Chunk) map[string]any {
	matched := 0
	for index := 0; index < len(primary) && index < len(shadow); index++ {
		if primary[index].Content == shadow[index].Content && primary[index].SectionTitle == shadow[index].SectionTitle && samePage(primary[index].PageNumber, shadow[index].PageNumber) {
			matched++
		}
	}
	denominator := len(primary)
	if len(shadow) > denominator {
		denominator = len(shadow)
	}
	boundaryRatio := 1.0
	if denominator > 0 {
		boundaryRatio = float64(matched) / float64(denominator)
	}
	primaryTokens, shadowTokens := 0, 0
	for _, chunk := range primary {
		primaryTokens += chunk.TokenCount
	}
	for _, chunk := range shadow {
		shadowTokens += chunk.TokenCount
	}
	return map[string]any{
		"shadow_chunks":           len(shadow),
		"boundary_match_ratio":    boundaryRatio,
		"primary_token_count":     primaryTokens,
		"shadow_token_count":      shadowTokens,
		"primary_overlap_chars":   overlapCharacters(primary),
		"shadow_overlap_chars":    overlapCharacters(shadow),
		"primary_metadata_chunks": metadataChunks(primary),
		"shadow_metadata_chunks":  metadataChunks(shadow),
	}
}

func overlapCharacters(chunks []Chunk) int {
	total := 0
	for index := 1; index < len(chunks); index++ {
		left := []rune(chunks[index-1].Content)
		right := []rune(chunks[index].Content)
		limit := min(len(left), len(right))
		// ponytail: shadow-only quadratic scan; use rolling hashes if chunk sizes become large.
		for size := limit; size > 0; size-- {
			if slices.Equal(left[len(left)-size:], right[:size]) {
				total += size
				break
			}
		}
	}
	return total
}

func metadataChunks(chunks []Chunk) int {
	count := 0
	for _, chunk := range chunks {
		if len(chunk.Metadata) > 0 {
			count++
		}
	}
	return count
}

func samePage(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
