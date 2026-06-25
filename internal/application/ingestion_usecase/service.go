package ingestion_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/chunker"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/parser"

	"gorm.io/gorm"
)

type FileStorage interface {
	Get(ctx context.Context, objectKey string) (io.ReadCloser, error)
}

type Service struct {
	kbs       knowledge.KnowledgeBaseRepository
	documents knowledge.DocumentRepository
	chunks    knowledge.ChunkRepository
	jobs      knowledge.IngestionJobRepository
	providers providerdomain.Repository
	storage   FileStorage
	parser    parser.Parser
	chunker   *chunker.FixedTokenChunker
	indexer   retrieval.Indexer
	embedder  llm.EmbeddingClient
	secrets   *cryptoinfra.SecretBox
	indexName string
}

func NewService(
	kbs knowledge.KnowledgeBaseRepository,
	documents knowledge.DocumentRepository,
	chunks knowledge.ChunkRepository,
	jobs knowledge.IngestionJobRepository,
	providers providerdomain.Repository,
	storage FileStorage,
	parser parser.Parser,
	chunker *chunker.FixedTokenChunker,
	indexer retrieval.Indexer,
	embedder llm.EmbeddingClient,
	secrets *cryptoinfra.SecretBox,
	indexName string,
) *Service {
	if indexName == "" {
		indexName = "agentcanvas_chunks_v1"
	}
	return &Service{
		kbs:       kbs,
		documents: documents,
		chunks:    chunks,
		jobs:      jobs,
		providers: providers,
		storage:   storage,
		parser:    parser,
		chunker:   chunker,
		indexer:   indexer,
		embedder:  embedder,
		secrets:   secrets,
		indexName: indexName,
	}
}

func (s *Service) ProcessNext(ctx context.Context, workerID string) (bool, error) {
	job, err := s.jobs.ClaimNext(ctx, workerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := s.ProcessJob(ctx, job); err != nil {
		_ = s.failJob(ctx, job, err)
		return true, err
	}
	return true, nil
}

func (s *Service) ProcessJob(ctx context.Context, job *knowledge.IngestionJob) error {
	doc, err := s.documents.FindByID(ctx, job.OwnerID, job.DocumentID)
	if err != nil {
		return err
	}
	kb, err := s.kbs.FindByID(ctx, job.OwnerID, job.KBID)
	if err != nil {
		return err
	}

	doc.ParserStatus = knowledge.DocumentStatusParsing
	doc.ParserError = ""
	if err := s.documents.Update(ctx, doc); err != nil {
		return err
	}

	reader, err := s.storage.Get(ctx, doc.ObjectKey)
	if err != nil {
		return err
	}
	defer reader.Close()

	parsed, err := s.parser.Parse(ctx, doc.OriginalFilename, reader)
	if err != nil {
		return err
	}

	doc.ParserStatus = knowledge.DocumentStatusChunking
	if err := s.documents.Update(ctx, doc); err != nil {
		return err
	}

	parts := s.chunker.Chunk(parsed.Text, kb.ChunkSize, kb.ChunkOverlap)
	if len(parts) == 0 {
		return fmt.Errorf("document has no parseable text")
	}

	existing, err := s.chunks.ListByDocument(ctx, job.OwnerID, job.DocumentID)
	if err != nil {
		return err
	}
	if err := s.chunks.DeleteByDocument(ctx, job.OwnerID, job.DocumentID); err != nil {
		return err
	}
	_ = s.indexer.DeleteByDocument(ctx, job.OwnerID, job.DocumentID)

	chunks := make([]knowledge.DocumentChunk, 0, len(parts))
	totalTokens := 0
	for _, part := range parts {
		totalTokens += part.TokenCount
		hash := sha256.Sum256([]byte(part.Content))
		chunks = append(chunks, knowledge.DocumentChunk{
			OwnerID:      job.OwnerID,
			KBID:         job.KBID,
			DocumentID:   job.DocumentID,
			ChunkIndex:   part.Index,
			Content:      part.Content,
			ContentHash:  hex.EncodeToString(hash[:]),
			TokenCount:   part.TokenCount,
			CharCount:    part.CharCount,
			SectionTitle: part.SectionTitle,
			MetadataJSON: "{}",
		})
	}
	if err := s.chunks.CreateBatch(ctx, chunks); err != nil {
		return err
	}

	doc.ParserStatus = knowledge.DocumentStatusIndexing
	if err := s.documents.Update(ctx, doc); err != nil {
		return err
	}
	if err := s.indexer.EnsureIndex(ctx); err != nil {
		return err
	}

	embeddings, embeddingModel, embeddingDimensions, err := s.embedChunks(ctx, kb, chunks)
	if err != nil {
		return err
	}
	if embeddingDimensions > 0 && kb.EmbeddingDimensions == 0 {
		kb.EmbeddingDimensions = embeddingDimensions
		if err := s.kbs.Update(ctx, kb); err != nil {
			return err
		}
	}

	indexDocs := make([]retrieval.ChunkIndexDocument, 0, len(chunks))
	now := time.Now().UTC()
	for i := range chunks {
		chunks[i].ESIndex = s.indexName
		chunks[i].ESDocID = strconv.FormatInt(chunks[i].ID, 10)
		metadata := map[string]any{"source": "upload"}
		_ = json.Unmarshal([]byte(chunks[i].MetadataJSON), &metadata)
		indexDocs = append(indexDocs, retrieval.ChunkIndexDocument{
			OwnerID:             chunks[i].OwnerID,
			KBID:                chunks[i].KBID,
			DocumentID:          chunks[i].DocumentID,
			ChunkID:             chunks[i].ID,
			ChunkIndex:          chunks[i].ChunkIndex,
			DocumentName:        doc.Name,
			FileType:            doc.FileType,
			SectionTitle:        chunks[i].SectionTitle,
			Content:             chunks[i].Content,
			ContentHash:         chunks[i].ContentHash,
			Enabled:             doc.Enabled,
			EmbeddingModel:      embeddingModel,
			EmbeddingDimensions: embeddingDimensions,
			PageNo:              chunks[i].PageNo,
			TokenCount:          chunks[i].TokenCount,
			Metadata:            metadata,
			CreatedAt:           chunks[i].CreatedAt,
			UpdatedAt:           now,
		})
		if len(embeddings) > i {
			indexDocs[i].EmbeddingVector = embeddings[i]
		}
	}
	if err := s.indexer.IndexChunks(ctx, indexDocs); err != nil {
		return err
	}
	if err := s.chunks.UpdateIndexRefs(ctx, chunks); err != nil {
		return err
	}

	indexedAt := time.Now().UTC()
	oldChunkCount := len(existing)
	doc.ParserStatus = knowledge.DocumentStatusCompleted
	doc.ParserError = ""
	doc.ChunkCount = len(chunks)
	doc.TokenCount = totalTokens
	doc.IndexedAt = &indexedAt
	if err := s.documents.Update(ctx, doc); err != nil {
		return err
	}
	if err := s.kbs.AdjustCounts(ctx, job.OwnerID, job.KBID, 0, len(chunks)-oldChunkCount); err != nil {
		return err
	}
	return s.jobs.MarkCompleted(ctx, job.ID)
}

func (s *Service) embedChunks(ctx context.Context, kb *knowledge.KnowledgeBase, chunks []knowledge.DocumentChunk) ([][]float32, string, int, error) {
	needsEmbedding := kb.RetrievalMode == knowledge.RetrievalModeVector || kb.RetrievalMode == knowledge.RetrievalModeHybrid
	if !needsEmbedding {
		return nil, "", 0, nil
	}
	if s.embedder == nil || s.providers == nil || s.secrets == nil || kb.EmbeddingProviderID == nil {
		return nil, "", 0, fmt.Errorf("embedding provider is required for %s retrieval", kb.RetrievalMode)
	}
	provider, err := s.providers.FindByID(ctx, kb.OwnerID, *kb.EmbeddingProviderID)
	if err != nil {
		return nil, "", 0, err
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, "", 0, fmt.Errorf("embedding provider is disabled")
	}
	model := strings.TrimSpace(kb.EmbeddingModel)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultEmbeddingModel)
	}
	if model == "" {
		return nil, "", 0, fmt.Errorf("embedding model is required")
	}
	input := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		input = append(input, chunk.Content)
	}
	apiKey, err := s.secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, "", 0, err
	}
	resp, err := s.embedder.Embed(ctx, llm.EmbeddingProviderConfig{ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey}, llm.EmbeddingRequest{Model: model, Input: input})
	if err != nil {
		return nil, "", 0, err
	}
	if len(resp.Embeddings) != len(chunks) {
		return nil, "", 0, fmt.Errorf("embedding count mismatch: got %d, want %d", len(resp.Embeddings), len(chunks))
	}
	dimensions := 0
	for _, vector := range resp.Embeddings {
		if len(vector) == 0 {
			return nil, "", 0, fmt.Errorf("embedding vector is empty")
		}
		if dimensions == 0 {
			dimensions = len(vector)
		}
		if len(vector) != dimensions {
			return nil, "", 0, fmt.Errorf("embedding dimensions are inconsistent")
		}
	}
	if kb.EmbeddingDimensions > 0 && dimensions != kb.EmbeddingDimensions {
		return nil, "", 0, fmt.Errorf("embedding dimensions mismatch: got %d, want %d", dimensions, kb.EmbeddingDimensions)
	}
	return resp.Embeddings, model, dimensions, nil
}

func (s *Service) failJob(ctx context.Context, job *knowledge.IngestionJob, cause error) error {
	final, err := s.jobs.MarkFailed(ctx, job.ID, cause.Error())
	if err != nil {
		return err
	}
	doc, err := s.documents.FindByID(ctx, job.OwnerID, job.DocumentID)
	if err == nil {
		if final {
			doc.ParserStatus = knowledge.DocumentStatusFailed
		} else {
			doc.ParserStatus = knowledge.DocumentStatusPending
		}
		doc.ParserError = cause.Error()
		_ = s.documents.Update(ctx, doc)
	}
	return nil
}
