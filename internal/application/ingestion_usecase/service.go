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
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/parser"
	"agentcanvas/internal/infrastructure/queue"

	"gorm.io/gorm"
)

type FileStorage interface {
	Get(ctx context.Context, objectKey string) (io.ReadCloser, error)
}

type DocumentChunker interface {
	Chunk(context.Context, string, parser.ParsedDocument, chunker.Policy) ([]chunker.Chunk, error)
}

type Service struct {
	kbs                 knowledge.KnowledgeBaseRepository
	documents           knowledge.DocumentRepository
	chunks              knowledge.ChunkRepository
	jobs                knowledge.IngestionJobRepository
	providers           providerdomain.Repository
	storage             FileStorage
	parser              parser.Parser
	chunkers            DocumentChunker
	indexer             retrieval.Indexer
	indexers            map[string]retrieval.Indexer
	embedder            llm.EmbeddingClient
	secrets             providerdomain.SecretCodec
	indexName           string
	generationCommitter knowledge.GenerationCommitter
}

func (s *Service) ConfigureIndexers(indexers map[string]retrieval.Indexer) *Service {
	s.indexers = make(map[string]retrieval.Indexer, len(indexers))
	for name, indexer := range indexers {
		if indexer != nil {
			s.indexers[strings.TrimSpace(name)] = indexer
		}
	}
	return s
}

func (s *Service) ConfigureGenerationCommitter(committer knowledge.GenerationCommitter) *Service {
	s.generationCommitter = committer
	return s
}

func NewService(
	kbs knowledge.KnowledgeBaseRepository,
	documents knowledge.DocumentRepository,
	chunks knowledge.ChunkRepository,
	jobs knowledge.IngestionJobRepository,
	providers providerdomain.Repository,
	storage FileStorage,
	parser parser.Parser,
	chunkers DocumentChunker,
	indexer retrieval.Indexer,
	embedder llm.EmbeddingClient,
	secrets providerdomain.SecretCodec,
	indexName string,
) *Service {
	if indexName == "" {
		indexName = "agentcanvas_chunks_v1"
	}
	if chunkers == nil {
		chunkers = chunker.NewDefaultRegistry()
	}
	return &Service{
		kbs:       kbs,
		documents: documents,
		chunks:    chunks,
		jobs:      jobs,
		providers: providers,
		storage:   storage,
		parser:    parser,
		chunkers:  chunkers,
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
	if err := s.processClaimedJob(ctx, job, true); err != nil {
		return true, errors.Join(err, s.failJob(context.WithoutCancel(ctx), job, err))
	}
	return true, nil
}

func (s *Service) ProcessJob(ctx context.Context, job *knowledge.IngestionJob) error {
	return s.processJob(ctx, job, true)
}

func (s *Service) ProcessNextFromQueue(ctx context.Context, jobs queue.JobQueue, workerID string) (bool, error) {
	claimed, err := jobs.Claim(ctx, queue.ClaimOptions{WorkerID: workerID, Limit: 1})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if len(claimed) == 0 {
		return false, nil
	}
	job, markDBComplete, businessClaimed, err := s.ingestionJobFromQueue(ctx, claimed[0], workerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, jobs.Ack(ctx, claimed[0].ID)
		}
		return true, errors.Join(err, jobs.Nack(ctx, claimed[0].ID, time.Now().Add(time.Minute)))
	}
	if !businessClaimed {
		return true, jobs.Ack(ctx, claimed[0].ID)
	}
	if err := s.processClaimedJob(ctx, job, markDBComplete); err != nil {
		var stateErr error
		if markDBComplete {
			stateErr = s.failJob(context.WithoutCancel(ctx), job, err)
		}
		return true, errors.Join(err, stateErr, jobs.Nack(ctx, claimed[0].ID, time.Now().Add(time.Minute)))
	}
	return true, jobs.Ack(ctx, claimed[0].ID)
}

func (s *Service) processClaimedJob(ctx context.Context, job *knowledge.IngestionJob, markDBComplete bool) error {
	reliable, ok := s.jobs.(knowledge.ReliableIngestionJobRepository)
	if !ok || job == nil || strings.TrimSpace(job.LockedBy) == "" {
		return s.processJob(ctx, job, markDBComplete)
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.heartbeatJob(execCtx, reliable, job.ID, job.LockedBy, cancel)
	return s.processJob(execCtx, job, markDBComplete)
}

func (s *Service) heartbeatJob(ctx context.Context, jobs knowledge.ReliableIngestionJobRepository, jobID int64, workerID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := jobs.RenewLock(ctx, jobID, workerID, now.UTC()); err != nil {
				cancel()
				return
			}
		}
	}
}

func (s *Service) processJob(ctx context.Context, job *knowledge.IngestionJob, markDBComplete bool) error {
	switch job.JobType {
	case "", knowledge.IngestionJobTypeDocument:
	case knowledge.IngestionJobTypeGenerationCleanup:
		return s.processGenerationCleanup(ctx, job, markDBComplete)
	default:
		return fmt.Errorf("unsupported ingestion job type %q", job.JobType)
	}
	doc, err := s.documents.FindByID(ctx, job.OwnerID, job.DocumentID)
	if err != nil {
		return err
	}
	kb, err := s.kbs.FindByID(ctx, job.OwnerID, job.KBID)
	if err != nil {
		return err
	}
	indexer := s.indexerFor(kb)
	if indexer == nil {
		return fmt.Errorf("retrieval indexer for backend %q is not configured", kb.RetrievalBackend)
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

	parts, err := s.chunkers.Chunk(ctx, kb.ChunkMethod, *parsed, chunker.Policy{ChunkSize: kb.ChunkSize, Overlap: kb.ChunkOverlap})
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("document has no parseable text")
	}

	existing, err := s.chunks.ListByDocument(ctx, job.OwnerID, job.DocumentID)
	if err != nil {
		return err
	}
	// Documents backfilled with active_generation use an append-and-switch
	// pipeline. Test and pre-migration repositories retain the legacy path until
	// the additive migration has been applied.
	safeGeneration := doc.ActiveGeneration != ""
	if !safeGeneration {
		if err := s.chunks.DeleteByDocument(ctx, job.OwnerID, job.DocumentID); err != nil {
			return err
		}
		if err := indexer.DeleteByDocument(ctx, job.OwnerID, job.DocumentID); err != nil {
			return err
		}
	}
	generation := doc.ActiveGeneration
	if safeGeneration {
		generation = fmt.Sprintf("gen-%d", time.Now().UTC().UnixNano())
	}

	chunks := make([]knowledge.DocumentChunk, 0, len(parts))
	totalTokens := 0
	for _, part := range parts {
		totalTokens += part.TokenCount
		hash := sha256.Sum256([]byte(part.Content))
		chunks = append(chunks, knowledge.DocumentChunk{
			OwnerID:      job.OwnerID,
			KBID:         job.KBID,
			DocumentID:   job.DocumentID,
			Generation:   generation,
			ChunkIndex:   part.Index,
			Content:      part.Content,
			ContentHash:  hex.EncodeToString(hash[:]),
			TokenCount:   part.TokenCount,
			CharCount:    part.CharCount,
			PageNo:       part.PageNo,
			SectionTitle: part.SectionTitle,
			MetadataJSON: chunkMetadataJSON(part.Metadata),
		})
	}
	if err := s.chunks.CreateBatch(ctx, chunks); err != nil {
		return err
	}

	doc.ParserStatus = knowledge.DocumentStatusIndexing
	if err := s.documents.Update(ctx, doc); err != nil {
		return err
	}
	if err := indexer.EnsureIndex(ctx); err != nil {
		return err
	}

	embeddings, embeddingModel, embeddingDimensions, err := s.embedChunks(ctx, kb, chunks)
	if err != nil {
		return err
	}
	profileChanged := false
	if embeddingDimensions > 0 && kb.EmbeddingDimensions == 0 {
		kb.EmbeddingDimensions = embeddingDimensions
		profileChanged = true
	}
	if strings.TrimSpace(kb.EmbeddingMetric) == "" {
		kb.EmbeddingMetric = knowledge.EmbeddingMetricCosine
		profileChanged = true
	}
	if profileChanged {
		if err := s.kbs.Update(ctx, kb); err != nil {
			return err
		}
	}
	embeddingProfile := ""
	if embeddingDimensions > 0 {
		embeddingProfile = kb.EmbeddingProfile().Key()
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
			Generation:          chunks[i].Generation,
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
			EmbeddingProviderID: int64PtrValue(kb.EmbeddingProviderID),
			EmbeddingMetric:     knowledge.NormalizeEmbeddingMetric(kb.EmbeddingMetric),
			EmbeddingProfile:    embeddingProfile,
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
	if err := indexer.IndexChunks(ctx, indexDocs); err != nil {
		return err
	}
	if err := s.chunks.UpdateIndexRefs(ctx, chunks); err != nil {
		return err
	}

	indexedAt := time.Now().UTC()
	oldChunkCount := len(existing)
	if safeGeneration {
		oldChunkCount = 0
		for _, oldChunk := range existing {
			if oldChunk.Generation == doc.ActiveGeneration {
				oldChunkCount++
			}
		}
	}
	doc.ParserStatus = knowledge.DocumentStatusCompleted
	doc.ParserError = ""
	doc.ChunkCount = len(chunks)
	doc.TokenCount = totalTokens
	doc.IndexedAt = &indexedAt
	if safeGeneration {
		doc.ActiveGeneration = generation
	}
	chunkDelta := len(chunks) - oldChunkCount
	if safeGeneration {
		if s.generationCommitter == nil {
			return fmt.Errorf("generation committer is not configured")
		}
		cleanup := &knowledge.IngestionJob{
			OwnerID: job.OwnerID, KBID: job.KBID, DocumentID: job.DocumentID,
			JobType: knowledge.IngestionJobTypeGenerationCleanup, Status: knowledge.IngestionJobStatusPending,
			Priority: -10, MaxAttempts: 5,
		}
		if err := s.generationCommitter.Activate(ctx, doc, cleanup, chunkDelta); err != nil {
			return err
		}
	} else {
		if err := s.documents.Update(ctx, doc); err != nil {
			return err
		}
		if err := s.kbs.AdjustCounts(ctx, job.OwnerID, job.KBID, 0, chunkDelta); err != nil {
			return err
		}
	}
	if markDBComplete {
		return s.markJobCompleted(ctx, job)
	}
	return nil
}

func (s *Service) ingestionJobFromQueue(ctx context.Context, job queue.Job, workerID string) (*knowledge.IngestionJob, bool, bool, error) {
	ownerID := queuePayloadInt64(job.Payload, "owner_id")
	jobID := queuePayloadInt64(job.Payload, "ingestion_job_id")
	if jobID == 0 {
		jobID = queuePayloadInt64(job.Payload, "job_id")
	}
	if ownerID > 0 && jobID > 0 {
		item, claimed, err := s.jobs.ClaimByID(ctx, ownerID, jobID, workerID)
		return item, true, claimed, err
	}
	item := &knowledge.IngestionJob{
		OwnerID:    ownerID,
		KBID:       queuePayloadInt64(job.Payload, "kb_id"),
		DocumentID: queuePayloadInt64(job.Payload, "document_id"),
		JobType:    job.Type,
	}
	if item.JobType == "" {
		item.JobType = knowledge.IngestionJobTypeDocument
	}
	if item.OwnerID == 0 || item.KBID == 0 || item.DocumentID == 0 {
		return nil, false, false, fmt.Errorf("queue job payload must include owner_id, kb_id and document_id")
	}
	return item, false, true, nil
}

func (s *Service) processGenerationCleanup(ctx context.Context, job *knowledge.IngestionJob, markDBComplete bool) error {
	doc, err := s.documents.FindByID(ctx, job.OwnerID, job.DocumentID)
	if err != nil {
		return err
	}
	activeGeneration := strings.TrimSpace(doc.ActiveGeneration)
	if activeGeneration == "" {
		return fmt.Errorf("document %d has no active generation", doc.ID)
	}
	kb, err := s.kbs.FindByID(ctx, job.OwnerID, job.KBID)
	if err != nil {
		return err
	}
	indexCleaner, ok := s.indexerFor(kb).(interface {
		DeleteInactiveGenerations(context.Context, int64, int64, string) error
	})
	if !ok {
		return fmt.Errorf("retrieval backend %q does not support generation cleanup", kb.RetrievalBackend)
	}
	chunkCleaner, ok := s.chunks.(interface {
		DeleteInactiveGenerations(context.Context, int64, int64, string) error
	})
	if !ok {
		return fmt.Errorf("chunk repository does not support generation cleanup")
	}
	if err := indexCleaner.DeleteInactiveGenerations(ctx, job.OwnerID, job.DocumentID, activeGeneration); err != nil {
		return err
	}
	if err := chunkCleaner.DeleteInactiveGenerations(ctx, job.OwnerID, job.DocumentID, activeGeneration); err != nil {
		return err
	}
	if markDBComplete {
		return s.markJobCompleted(ctx, job)
	}
	return nil
}

func (s *Service) indexerFor(kb *knowledge.KnowledgeBase) retrieval.Indexer {
	if kb == nil {
		return nil
	}
	backendName := strings.TrimSpace(kb.RetrievalBackend)
	if selected, ok := s.indexers[backendName]; ok {
		return selected
	}
	// Keep the constructor's single-indexer path when dispatch has not been
	// configured (legacy/unit-test wiring). Once the registry is present, a
	// declared backend must never silently fall back to a different adapter.
	if len(s.indexers) == 0 {
		return s.indexer
	}
	return nil
}

func int64PtrValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func queuePayloadInt64(payload map[string]any, key string) int64 {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	default:
		parsed, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return parsed
	}
}

func chunkMetadataJSON(metadata map[string]any) string {
	if metadata == nil {
		return "{}"
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return "{}"
	}
	return string(data)
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
	var final bool
	var err error
	if reliable, ok := s.jobs.(knowledge.ReliableIngestionJobRepository); ok && strings.TrimSpace(job.LockedBy) != "" {
		final, err = reliable.MarkFailedOwned(ctx, job.ID, job.LockedBy, cause.Error())
	} else {
		final, err = s.jobs.MarkFailed(ctx, job.ID, cause.Error())
	}
	if err != nil {
		return err
	}
	if job.JobType == knowledge.IngestionJobTypeGenerationCleanup {
		return nil
	}
	doc, err := s.documents.FindByID(ctx, job.OwnerID, job.DocumentID)
	if err != nil {
		return err
	}
	if final {
		doc.ParserStatus = knowledge.DocumentStatusFailed
	} else {
		doc.ParserStatus = knowledge.DocumentStatusPending
	}
	doc.ParserError = cause.Error()
	return s.documents.Update(ctx, doc)
}

func (s *Service) markJobCompleted(ctx context.Context, job *knowledge.IngestionJob) error {
	if reliable, ok := s.jobs.(knowledge.ReliableIngestionJobRepository); ok && job != nil && strings.TrimSpace(job.LockedBy) != "" {
		return reliable.MarkCompletedOwned(ctx, job.ID, job.LockedBy)
	}
	return s.jobs.MarkCompleted(ctx, job.ID)
}
