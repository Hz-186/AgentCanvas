package ingestion_usecase

import (
	"agentcanvas/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/chunker"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/parser"
	pythonbridgeinfra "agentcanvas/internal/infrastructure/pythonbridge"
	"agentcanvas/internal/infrastructure/queue"

	"gorm.io/gorm"
)

func TestProcessJobViaLivePythonBridge(t *testing.T) {
	target := os.Getenv("AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET")
	if target == "" {
		t.Skip("AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET is not set")
	}
	bridge, err := pythonbridgeinfra.NewClient(pythonbridgeinfra.Config{
		Enabled: true, Target: target, AuthToken: os.Getenv("AGENTCANVAS_PYTHON_BRIDGE_TOKEN"),
		ConnectTimeout: 3 * time.Second, RequestTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	registry := chunker.NewDefaultRegistry()
	registry.Register(pythonbridgeinfra.NewChunker(bridge, "python:langchain_recursive"))
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalMode: knowledge.RetrievalModeKeyword,
		ChunkMethod: "python:langchain_recursive", ChunkSize: 12, ChunkOverlap: 2,
	}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Bridge", OriginalFilename: "bridge.md", FileType: "md",
		StorageObjectKey: "raw/bridge.md", IngestionStatus: knowledge.DocumentStatusPending, Enabled: true,
	}}}
	chunks, indexer := &fakeChunkRepo{}, &fakeIndexer{}
	service := NewService(
		kbs, docs, chunks, &fakeJobRepo{}, nil,
		fakeReadStorage{objects: map[string]string{"raw/bridge.md": "# Bridge\n\n第一段内容。第二段内容。"}},
		parser.NewTextParser(), registry, indexer, nil, nil, "test_chunks",
	)
	if err := service.ProcessJob(context.Background(), &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20}); err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}
	if docs.items[20].IngestionStatus != knowledge.DocumentStatusCompleted || len(chunks.byDocument[20]) == 0 || len(indexer.indexed) == 0 {
		t.Fatalf("Python ingestion did not complete: doc=%+v chunks=%+v indexed=%+v", docs.items[20], chunks.byDocument[20], indexer.indexed)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(chunks.byDocument[20][0].MetadataJSON), &metadata); err != nil || metadata["chunk_method"] != "python:langchain_recursive" {
		t.Fatalf("unexpected Python chunk metadata: metadata=%+v error=%v", metadata, err)
	}
}

func TestProcessJobParsesChunksIndexesAndCompletes(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}},
				ChunkMethod:     knowledge.ChunkMethodRecursive,
				ChunkSize:       20,
				ChunkOverlap:    0,
			},
		},
	}
	docs := &fakeDocumentRepo{
		items: map[int64]*knowledge.Document{
			20: {
				SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}},
				KnowledgeBaseID:  10,
				Name:             "Guide",
				OriginalFilename: "guide.md",
				FileType:         "md",
				StorageObjectKey: "raw/guide.md",
				IngestionStatus:  knowledge.DocumentStatusPending,
				Enabled:          true,
			},
		},
	}
	chunks := &fakeChunkRepo{}
	jobs := &fakeJobRepo{}
	storage := fakeReadStorage{objects: map[string]string{
		"raw/guide.md": "# Intro\nAgentCanvas knowledge retrieval supports txt and md files.",
	}}
	indexer := &fakeIndexer{}

	service := NewService(
		kbs,
		docs,
		chunks,
		jobs,
		nil,
		storage,
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		indexer,
		nil,
		nil,
		"test_chunks",
	)

	err := service.ProcessJob(ctx, &knowledge.IngestionJob{
		BaseModel:       domain.BaseModel{ID: 30, OwnerID: 1},
		KnowledgeBaseID: 10,
		DocumentID:      20,
	})
	if err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}

	doc := docs.items[20]
	if doc.IngestionStatus != knowledge.DocumentStatusCompleted {
		t.Fatalf("ParserStatus = %q, want completed", doc.IngestionStatus)
	}
	if doc.ChunkCount == 0 {
		t.Fatal("ChunkCount = 0, want chunks")
	}
	if doc.IndexedAt == nil {
		t.Fatal("IndexedAt = nil, want indexed timestamp")
	}
	if !jobs.completed[30] {
		t.Fatal("job was not marked completed")
	}
	if !indexer.ensureCalled {
		t.Fatal("indexer.EnsureIndex was not called")
	}
	if len(indexer.indexed) != doc.ChunkCount {
		t.Fatalf("indexed chunks = %d, want %d", len(indexer.indexed), doc.ChunkCount)
	}
	if indexer.indexed[0].DocumentName != "Guide" {
		t.Fatalf("indexed document name = %q, want Guide", indexer.indexed[0].DocumentName)
	}
	for _, indexed := range indexer.indexed {
		if !indexed.Enabled {
			t.Fatal("indexed chunk should carry enabled=true from an enabled document")
		}
	}
	for _, chunk := range chunks.byDocument[20] {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(chunk.MetadataJSON), &metadata); err != nil {
			t.Fatalf("MetadataJSON = %q: %v", chunk.MetadataJSON, err)
		}
		if metadata["chunk_method"] != "recursive" || metadata["tokenizer"] != "estimated" {
			t.Fatalf("metadata = %#v", metadata)
		}
	}
	if kbs.chunkDelta != doc.ChunkCount {
		t.Fatalf("kb chunk delta = %d, want %d", kbs.chunkDelta, doc.ChunkCount)
	}
}

func TestProcessJobReplacesExistingChunksIdempotently(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}},
				ChunkSize:       20,
				ChunkOverlap:    0,
			},
		},
	}
	docs := &fakeDocumentRepo{
		items: map[int64]*knowledge.Document{
			20: {
				SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}},
				KnowledgeBaseID:  10,
				Name:             "Guide",
				OriginalFilename: "guide.md",
				FileType:         "md",
				StorageObjectKey: "raw/guide.md",
				IngestionStatus:  knowledge.DocumentStatusCompleted,
				ChunkCount:       2,
			},
		},
	}
	chunks := &fakeChunkRepo{
		byDocument: map[int64][]knowledge.DocumentChunk{
			20: {
				{ImmutableModel: domain.ImmutableModel{ID: 1, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, ChunkIndex: 0, Content: "old one"},
				{ImmutableModel: domain.ImmutableModel{ID: 2, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, ChunkIndex: 1, Content: "old two"},
			},
		},
	}
	indexer := &fakeIndexer{}
	service := NewService(
		kbs,
		docs,
		chunks,
		&fakeJobRepo{},
		nil,
		fakeReadStorage{objects: map[string]string{
			"raw/guide.md": "Fresh content for a single replacement chunk.",
		}},
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		indexer,
		nil,
		nil,
		"test_chunks",
	)

	err := service.ProcessJob(ctx, &knowledge.IngestionJob{
		BaseModel:       domain.BaseModel{ID: 30, OwnerID: 1},
		KnowledgeBaseID: 10,
		DocumentID:      20,
	})
	if err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}

	doc := docs.items[20]
	if doc.ChunkCount != 1 {
		t.Fatalf("ChunkCount = %d, want 1", doc.ChunkCount)
	}
	if chunks.deletedByDocument[20] != 1 {
		t.Fatalf("DeleteByDocument calls = %d, want 1", chunks.deletedByDocument[20])
	}
	if indexer.deletedByDocument[20] != 1 {
		t.Fatalf("DeleteByDocument index calls = %d, want 1", indexer.deletedByDocument[20])
	}
	if kbs.chunkDelta != -1 {
		t.Fatalf("kb chunk delta = %d, want -1", kbs.chunkDelta)
	}
}

func TestProcessNextMarksJobAndDocumentFailed(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, ChunkSize: 20},
	}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {
			SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}},
			KnowledgeBaseID:  10,
			OriginalFilename: "guide.md",
			StorageObjectKey: "missing.md",
			IngestionStatus:  knowledge.DocumentStatusPending,
		},
	}}
	jobs := &fakeJobRepo{
		next: &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20},
	}
	service := NewService(
		kbs,
		docs,
		&fakeChunkRepo{},
		jobs,
		nil,
		fakeReadStorage{objects: map[string]string{}},
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		&fakeIndexer{},
		nil,
		nil,
		"test_chunks",
	)

	processed, err := service.ProcessNext(ctx, "worker-1")
	if !processed {
		t.Fatal("processed = false, want true")
	}
	if err == nil {
		t.Fatal("ProcessNext() error = nil, want storage error")
	}
	if jobs.failed[30] == "" {
		t.Fatal("job was not marked failed")
	}
	if docs.items[20].IngestionStatus != knowledge.DocumentStatusFailed {
		t.Fatalf("ParserStatus = %q, want failed", docs.items[20].IngestionStatus)
	}
	if docs.items[20].IngestionError == "" {
		t.Fatal("ParserError is empty")
	}
}

func TestProcessNextRetriesJobBeforeMaxAttempts(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, ChunkSize: 20},
	}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {
			SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}},
			KnowledgeBaseID:  10,
			OriginalFilename: "guide.md",
			StorageObjectKey: "missing.md",
			IngestionStatus:  knowledge.DocumentStatusPending,
		},
	}}
	jobs := &fakeJobRepo{
		next: &knowledge.IngestionJob{
			BaseModel:       domain.BaseModel{ID: 30, OwnerID: 1},
			KnowledgeBaseID: 10,
			DocumentID:      20,
			AttemptCount:    1,
			MaxAttempts:     3,
		},
	}
	service := NewService(
		kbs,
		docs,
		&fakeChunkRepo{},
		jobs,
		nil,
		fakeReadStorage{objects: map[string]string{}},
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		&fakeIndexer{},
		nil,
		nil,
		"test_chunks",
	)

	processed, err := service.ProcessNext(ctx, "worker-1")
	if !processed {
		t.Fatal("processed = false, want true")
	}
	if err == nil {
		t.Fatal("ProcessNext() error = nil, want storage error")
	}
	if jobs.retrying[30] == "" {
		t.Fatal("job was not released for retry")
	}
	if jobs.failed[30] != "" {
		t.Fatalf("job was marked failed before max attempts: %q", jobs.failed[30])
	}
	if docs.items[20].IngestionStatus == knowledge.DocumentStatusFailed {
		t.Fatal("document was marked failed before max attempts")
	}
	if docs.items[20].IngestionError == "" {
		t.Fatal("ParserError is empty")
	}
}

func TestProcessNextReturnsJobStatePersistenceError(t *testing.T) {
	jobs := &fakeJobRepo{
		next:          &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20},
		markFailedErr: errors.New("persist failed state"),
	}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, ChunkSize: 20}}},
		&fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, OriginalFilename: "guide.md", StorageObjectKey: "missing.md"}}},
		&fakeChunkRepo{}, jobs, nil, fakeReadStorage{}, parser.NewTextParser(), chunker.NewDefaultRegistry(), &fakeIndexer{}, nil, nil, "test_chunks",
	)

	processed, err := service.ProcessNext(context.Background(), "worker")
	if !processed || err == nil || !strings.Contains(err.Error(), "object not found") || !strings.Contains(err.Error(), "persist failed state") {
		t.Fatalf("ProcessNext() = %v, %v", processed, err)
	}
}

func TestProcessNextFromQueueProcessesDirectPayloadAndAcks(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, ChunkSize: 20, ChunkOverlap: 0}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Guide", OriginalFilename: "guide.md", FileType: "md", StorageObjectKey: "raw/guide.md", IngestionStatus: knowledge.DocumentStatusPending, Enabled: true}}}
	jobs := &fakeJobRepo{}
	service := NewService(
		kbs,
		docs,
		&fakeChunkRepo{},
		jobs,
		nil,
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "queue driven ingestion content"}},
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		&fakeIndexer{},
		nil,
		nil,
		"test_chunks",
	)
	q := &fakeQueue{jobs: []queue.Job{{ID: "stream-1", Type: knowledge.IngestionJobTypeDocument, Payload: map[string]any{"owner_id": int64(1), "knowledge_base_id": int64(10), "document_id": int64(20)}}}}
	processed, err := service.ProcessNextFromQueue(ctx, q, "worker-1")
	if err != nil || !processed {
		t.Fatalf("ProcessNextFromQueue() = %v, %v", processed, err)
	}
	if len(q.acked) != 1 || q.acked[0] != "stream-1" || len(q.nacked) != 0 {
		t.Fatalf("expected queue ack only, got ack=%+v nack=%+v", q.acked, q.nacked)
	}
	if len(jobs.completed) != 0 {
		t.Fatalf("direct queue payload should not mark a DB job completed, got %+v", jobs.completed)
	}
	if docs.items[20].IngestionStatus != knowledge.DocumentStatusCompleted {
		t.Fatalf("document status = %s, want completed", docs.items[20].IngestionStatus)
	}
}

func TestProcessNextFromQueueAcksTerminalDatabaseJob(t *testing.T) {
	jobs := &fakeJobRepo{items: map[int64]*knowledge.IngestionJob{30: {BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, Status: knowledge.IngestionJobStatusCompleted}}}
	service := NewService(nil, nil, nil, jobs, nil, nil, nil, nil, nil, nil, nil, "")
	q := &fakeQueue{jobs: []queue.Job{{ID: "stream-1", Type: knowledge.IngestionJobTypeDocument, Payload: map[string]any{
		"owner_id": int64(1), "ingestion_job_id": int64(30),
	}}}}

	processed, err := service.ProcessNextFromQueue(context.Background(), q, "worker")
	if err != nil || !processed || len(q.acked) != 1 || len(q.nacked) != 0 {
		t.Fatalf("ProcessNextFromQueue() = %v, %v ack=%+v nack=%+v", processed, err, q.acked, q.nacked)
	}
}

func TestProcessNextFromQueueReturnsNackError(t *testing.T) {
	service := NewService(nil, nil, nil, &fakeJobRepo{}, nil, nil, nil, nil, nil, nil, nil, "")
	q := &fakeQueue{
		jobs:    []queue.Job{{ID: "stream-1", Type: knowledge.IngestionJobTypeDocument, Payload: map[string]any{}}},
		nackErr: errors.New("nack failed"),
	}

	processed, err := service.ProcessNextFromQueue(context.Background(), q, "worker")
	if !processed || err == nil || !strings.Contains(err.Error(), "payload") || !strings.Contains(err.Error(), "nack failed") {
		t.Fatalf("ProcessNextFromQueue() = %v, %v", processed, err)
	}
}

func TestClaimedIngestionJobUsesOwnedTerminalUpdates(t *testing.T) {
	repo := &fakeReliableJobRepo{fakeJobRepo: &fakeJobRepo{}}
	service := NewService(nil, nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, "")
	job := &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30}, LockedBy: "worker-1"}
	if err := service.markJobCompleted(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.completedBy != "worker-1" || !repo.completed[30] {
		t.Fatalf("owned completion = worker %q completed=%+v", repo.completedBy, repo.completed)
	}
	repo.leaseErr = knowledge.ErrIngestionLeaseLost
	if err := service.markJobCompleted(context.Background(), job); !errors.Is(err, knowledge.ErrIngestionLeaseLost) {
		t.Fatalf("lost lease completion error = %v", err)
	}
}

func TestProcessJobIndexesEmbeddingVectors(t *testing.T) {
	ctx := context.Background()
	providerID := int64(90)
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				SoftDeleteModel:     domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}},
				RetrievalMode:       knowledge.RetrievalModeVector,
				EmbeddingProviderID: &providerID,
				EmbeddingModel:      "text-embedding",
				EmbeddingDimensions: 2,
				ChunkSize:           20,
				ChunkOverlap:        0,
			},
		},
	}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {
			SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}},
			KnowledgeBaseID:  10,
			Name:             "Guide",
			OriginalFilename: "guide.md",
			FileType:         "md",
			StorageObjectKey: "raw/guide.md",
			IngestionStatus:  knowledge.DocumentStatusPending,
		},
	}}
	indexer := &fakeIndexer{}
	service := NewService(
		kbs,
		docs,
		&fakeChunkRepo{},
		&fakeJobRepo{},
		&fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{
			90: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 90, OwnerID: 1}}, ProviderType: providerdomain.TypeOpenAICompatible, Enabled: providerdomain.ProviderEnabled},
		}},
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "Vector enabled retrieval content."}},
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		indexer,
		&fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}},
		mustSecretBox(t),
		"test_chunks",
	)

	if err := service.ProcessJob(ctx, &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20}); err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}
	if len(indexer.indexed) != 1 {
		t.Fatalf("indexed chunks = %d, want 1", len(indexer.indexed))
	}
	indexed := indexer.indexed[0]
	if indexed.EmbeddingModel != "text-embedding" || indexed.EmbeddingDimensions != 2 || indexed.EmbeddingProviderID != 90 || indexed.EmbeddingMetric != knowledge.EmbeddingMetricCosine || indexed.EmbeddingProfile == "" {
		t.Fatalf("embedding metadata = %#v", indexed)
	}
	if len(indexed.EmbeddingVector) != 2 || indexed.EmbeddingVector[0] != 0.1 {
		t.Fatalf("embedding vector = %#v", indexed.EmbeddingVector)
	}
}

func TestProcessJobKeywordDoesNotRequireEmbeddingProvider(t *testing.T) {
	ctx := context.Background()
	providerID := int64(90)
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				SoftDeleteModel:     domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}},
				RetrievalMode:       knowledge.RetrievalModeKeyword,
				EmbeddingProviderID: &providerID,
				EmbeddingModel:      "text-embedding",
				EmbeddingDimensions: 20,
				ChunkSize:           20,
				ChunkOverlap:        0,
			},
		},
	}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {
			SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}},
			KnowledgeBaseID:  10,
			Name:             "Guide",
			OriginalFilename: "guide.md",
			FileType:         "md",
			StorageObjectKey: "raw/guide.md",
			IngestionStatus:  knowledge.DocumentStatusPending,
		},
	}}
	indexer := &fakeIndexer{}
	service := NewService(
		kbs,
		docs,
		&fakeChunkRepo{},
		&fakeJobRepo{},
		nil,
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "Keyword retrieval should index text without embeddings."}},
		parser.NewTextParser(),
		chunker.NewDefaultRegistry(),
		indexer,
		nil,
		nil,
		"test_chunks",
	)

	if err := service.ProcessJob(ctx, &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20}); err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}
	if docs.items[20].IngestionStatus != knowledge.DocumentStatusCompleted {
		t.Fatalf("ParserStatus = %q, want completed", docs.items[20].IngestionStatus)
	}
	if len(indexer.indexed) == 0 {
		t.Fatal("indexed chunks = 0, want keyword chunks indexed")
	}
	if indexer.indexed[0].EmbeddingDimensions != 0 || len(indexer.indexed[0].EmbeddingVector) != 0 {
		t.Fatalf("keyword index should not include embeddings: %#v", indexer.indexed[0])
	}
}

func TestProcessJobGenerationFailureKeepsActiveGeneration(t *testing.T) {
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendElasticsearch,
		RetrievalMode: knowledge.RetrievalModeKeyword, ChunkSize: 20,
	}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Guide", OriginalFilename: "guide.md", FileType: "md",
		StorageObjectKey: "raw/guide.md", IngestionStatus: knowledge.DocumentStatusCompleted, ActiveGenerationID: "gen-old", ChunkCount: 1,
	}}}
	oldChunk := knowledge.DocumentChunk{ImmutableModel: domain.ImmutableModel{ID: 1, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, GenerationID: "gen-old", Content: "old searchable content"}
	chunks := &fakeChunkRepo{nextID: 1, byDocument: map[int64][]knowledge.DocumentChunk{20: {oldChunk}}}
	indexer := &fakeIndexer{indexErr: errors.New("index failed")}
	service := NewService(
		kbs, docs, chunks, &fakeJobRepo{}, nil,
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "new content that must not replace the active generation"}},
		parser.NewTextParser(), chunker.NewDefaultRegistry(), indexer, nil, nil, "test_chunks",
	)

	err := service.ProcessJob(context.Background(), &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20})
	if err == nil || !strings.Contains(err.Error(), "index failed") {
		t.Fatalf("ProcessJob() error = %v", err)
	}
	if docs.items[20].ActiveGenerationID != "gen-old" {
		t.Fatalf("active generation = %q, want gen-old", docs.items[20].ActiveGenerationID)
	}
	if chunks.deletedByDocument[20] != 0 || indexer.deletedByDocument[20] != 0 {
		t.Fatalf("old generation was deleted: chunk deletes=%d index deletes=%d", chunks.deletedByDocument[20], indexer.deletedByDocument[20])
	}
	foundOld := false
	for _, chunk := range chunks.byDocument[20] {
		if chunk.ID == oldChunk.ID && chunk.GenerationID == "gen-old" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("old generation chunk was lost: %+v", chunks.byDocument[20])
	}
}

func TestGenerationFailuresAcrossPipelineKeepActiveGeneration(t *testing.T) {
	pipelineErr := errors.New("pipeline failed")
	tests := []struct {
		name    string
		backend string
		mode    string
		setup   func(*Service, *fakeChunkRepo, *fakeIndexer, *fakeEmbedder, *fakeGenerationCommitter)
	}{
		{name: "parse", setup: func(service *Service, _ *fakeChunkRepo, _ *fakeIndexer, _ *fakeEmbedder, _ *fakeGenerationCommitter) {
			service.parser = failingParser{pipelineErr}
		}},
		{name: "chunk", setup: func(service *Service, _ *fakeChunkRepo, _ *fakeIndexer, _ *fakeEmbedder, _ *fakeGenerationCommitter) {
			service.chunkers = failingChunker{pipelineErr}
		}},
		{name: "mysql chunks", setup: func(_ *Service, chunks *fakeChunkRepo, _ *fakeIndexer, _ *fakeEmbedder, _ *fakeGenerationCommitter) {
			chunks.createErr = pipelineErr
		}},
		{name: "embedding", mode: knowledge.RetrievalModeVector, setup: func(_ *Service, _ *fakeChunkRepo, _ *fakeIndexer, embedder *fakeEmbedder, _ *fakeGenerationCommitter) {
			embedder.err = pipelineErr
		}},
		{name: "elasticsearch", backend: knowledge.RetrievalBackendElasticsearch, setup: func(_ *Service, _ *fakeChunkRepo, indexer *fakeIndexer, _ *fakeEmbedder, _ *fakeGenerationCommitter) {
			indexer.indexErr = pipelineErr
		}},
		{name: "milvus", backend: knowledge.RetrievalBackendMilvus, setup: func(_ *Service, _ *fakeChunkRepo, indexer *fakeIndexer, _ *fakeEmbedder, _ *fakeGenerationCommitter) {
			indexer.indexErr = pipelineErr
		}},
		{name: "generation commit", setup: func(_ *Service, _ *fakeChunkRepo, _ *fakeIndexer, _ *fakeEmbedder, committer *fakeGenerationCommitter) {
			committer.err = pipelineErr
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := test.backend
			if backend == "" {
				backend = knowledge.RetrievalBackendElasticsearch
			}
			mode := test.mode
			if mode == "" {
				mode = knowledge.RetrievalModeKeyword
			}
			providerID := int64(90)
			kb := &knowledge.KnowledgeBase{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: backend, RetrievalMode: mode, ChunkSize: 20}
			if mode == knowledge.RetrievalModeVector {
				kb.EmbeddingProviderID, kb.EmbeddingModel, kb.EmbeddingDimensions = &providerID, "embedding", 2
			}
			kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: kb}}
			docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Guide", OriginalFilename: "guide.md", FileType: "md",
				StorageObjectKey: "raw/guide.md", IngestionStatus: knowledge.DocumentStatusCompleted, ActiveGenerationID: "gen-old", ChunkCount: 1,
			}}}
			oldChunk := knowledge.DocumentChunk{ImmutableModel: domain.ImmutableModel{ID: 1, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, GenerationID: "gen-old", Content: "old searchable content"}
			chunks := &fakeChunkRepo{nextID: 1, byDocument: map[int64][]knowledge.DocumentChunk{20: {oldChunk}}}
			indexer := &fakeIndexer{}
			embedder := &fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}}
			jobs := &fakeJobRepo{}
			committer := &fakeGenerationCommitter{documents: docs, kbs: kbs, jobs: jobs}
			service := NewService(
				kbs, docs, chunks, jobs,
				&fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{providerID: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: providerID, OwnerID: 1}}, ProviderType: providerdomain.TypeOpenAICompatible, Enabled: providerdomain.ProviderEnabled}}},
				fakeReadStorage{objects: map[string]string{"raw/guide.md": "new content"}}, parser.NewTextParser(), chunker.NewDefaultRegistry(), indexer, embedder, mustSecretBox(t), "test_chunks",
			).ConfigureIndexers(map[string]retrieval.Indexer{backend: indexer}).ConfigureGenerationCommitter(committer)
			test.setup(service, chunks, indexer, embedder, committer)

			if err := service.ProcessJob(context.Background(), &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20}); !errors.Is(err, pipelineErr) {
				t.Fatalf("ProcessJob() error = %v, want %v", err, pipelineErr)
			}
			if docs.items[20].ActiveGenerationID != "gen-old" || chunks.deletedByDocument[20] != 0 || indexer.deletedByDocument[20] != 0 {
				t.Fatalf("active data changed: doc=%+v chunk_deletes=%d index_deletes=%d", docs.items[20], chunks.deletedByDocument[20], indexer.deletedByDocument[20])
			}
			foundOld := false
			for _, chunk := range chunks.byDocument[20] {
				foundOld = foundOld || chunk.ID == oldChunk.ID && chunk.GenerationID == "gen-old"
			}
			if !foundOld {
				t.Fatalf("old generation chunk was lost: %+v", chunks.byDocument[20])
			}
		})
	}
}

type failingParser struct{ err error }

func (p failingParser) Parse(context.Context, string, io.Reader) (*parser.ParsedDocument, error) {
	return nil, p.err
}

type failingChunker struct{ err error }

func (c failingChunker) Chunk(context.Context, string, parser.ParsedDocument, chunker.Policy) ([]chunker.Chunk, error) {
	return nil, c.err
}

func TestProcessJobDispatchesIndexerByKnowledgeBaseBackend(t *testing.T) {
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendMilvus,
		RetrievalMode: knowledge.RetrievalModeKeyword, ChunkSize: 20,
	}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Guide", OriginalFilename: "guide.md", FileType: "md", StorageObjectKey: "raw/guide.md"}}}
	defaultIndexer := &fakeIndexer{}
	milvusIndexer := &fakeIndexer{}
	service := NewService(
		kbs, docs, &fakeChunkRepo{}, &fakeJobRepo{}, nil,
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "backend-specific indexing"}},
		parser.NewTextParser(), chunker.NewDefaultRegistry(), defaultIndexer, nil, nil, "test_chunks",
	).ConfigureIndexers(map[string]retrieval.Indexer{knowledge.RetrievalBackendMilvus: milvusIndexer})

	if err := service.ProcessJob(context.Background(), &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20}); err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}
	if !milvusIndexer.ensureCalled || len(milvusIndexer.indexed) == 0 {
		t.Fatalf("Milvus indexer was not used: %+v", milvusIndexer)
	}
	if defaultIndexer.ensureCalled || len(defaultIndexer.indexed) != 0 {
		t.Fatalf("default indexer was used: %+v", defaultIndexer)
	}
}

func TestProcessJobRejectsDeclaredBackendWithoutIndexer(t *testing.T) {
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendElasticsearch,
		RetrievalMode: knowledge.RetrievalModeKeyword, ChunkSize: 20,
	}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Guide", OriginalFilename: "guide.md", FileType: "md", StorageObjectKey: "raw/guide.md"}}}
	service := NewService(
		kbs, docs, &fakeChunkRepo{}, &fakeJobRepo{}, nil,
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "backend must be configured"}},
		parser.NewTextParser(), chunker.NewDefaultRegistry(), &fakeIndexer{}, nil, nil, "test_chunks",
	).ConfigureIndexers(map[string]retrieval.Indexer{knowledge.RetrievalBackendMilvus: &fakeIndexer{}})

	err := service.ProcessJob(context.Background(), &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20})
	if err == nil || !strings.Contains(err.Error(), "retrieval indexer for backend \"elasticsearch\" is not configured") {
		t.Fatalf("ProcessJob() error = %v", err)
	}
}

func TestProcessJobCommitsGenerationAndSchedulesCleanup(t *testing.T) {
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendElasticsearch,
		RetrievalMode: knowledge.RetrievalModeKeyword, ChunkSize: 20,
	}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Name: "Guide", OriginalFilename: "guide.md", FileType: "md",
		StorageObjectKey: "raw/guide.md", IngestionStatus: knowledge.DocumentStatusCompleted, ActiveGenerationID: "gen-old", ChunkCount: 1,
	}}}
	chunks := &fakeChunkRepo{nextID: 1, byDocument: map[int64][]knowledge.DocumentChunk{20: {{
		ImmutableModel: domain.ImmutableModel{ID: 1, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, GenerationID: "gen-old", Content: "old content",
	}}}}
	jobs := &fakeJobRepo{}
	committer := &fakeGenerationCommitter{documents: docs, kbs: kbs, jobs: jobs}
	service := NewService(
		kbs, docs, chunks, jobs, nil,
		fakeReadStorage{objects: map[string]string{"raw/guide.md": "new active generation content"}},
		parser.NewTextParser(), chunker.NewDefaultRegistry(), &fakeIndexer{}, nil, nil, "test_chunks",
	).ConfigureGenerationCommitter(committer)

	if err := service.ProcessJob(context.Background(), &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 30, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20}); err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}
	active := docs.items[20].ActiveGenerationID
	if active == "" || active == "gen-old" || committer.calls != 1 {
		t.Fatalf("generation commit: active=%q calls=%d", active, committer.calls)
	}
	if len(jobs.items) != 1 {
		t.Fatalf("cleanup jobs = %+v", jobs.items)
	}
	for _, cleanup := range jobs.items {
		if cleanup.JobType != knowledge.IngestionJobTypeGenerationCleanup || cleanup.DocumentID != 20 || cleanup.Status != knowledge.IngestionJobStatusPending {
			t.Fatalf("cleanup job = %+v", cleanup)
		}
	}
	if kbs.chunkDelta != docs.items[20].ChunkCount-1 {
		t.Fatalf("KB chunk delta = %d", kbs.chunkDelta)
	}
	if chunks.deletedByDocument[20] != 0 {
		t.Fatal("old generation was synchronously deleted")
	}
}

func TestProcessGenerationCleanupDeletesOnlyInactiveGenerations(t *testing.T) {
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendElasticsearch}}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, IngestionStatus: knowledge.DocumentStatusCompleted, ActiveGenerationID: "gen-new"}}}
	chunks := &fakeChunkRepo{byDocument: map[int64][]knowledge.DocumentChunk{20: {
		{ImmutableModel: domain.ImmutableModel{ID: 1, OwnerID: 1}, DocumentID: 20, GenerationID: "gen-old"},
		{ImmutableModel: domain.ImmutableModel{ID: 2, OwnerID: 1}, DocumentID: 20, GenerationID: "gen-new"},
	}}}
	jobs := &fakeJobRepo{}
	indexer := &fakeIndexer{}
	service := NewService(kbs, docs, chunks, jobs, nil, nil, nil, nil, indexer, nil, nil, "")

	cleanup := &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 31, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, JobType: knowledge.IngestionJobTypeGenerationCleanup}
	if err := service.ProcessJob(context.Background(), cleanup); err != nil {
		t.Fatalf("ProcessJob(cleanup) error = %v", err)
	}
	if err := service.ProcessJob(context.Background(), cleanup); err != nil {
		t.Fatalf("ProcessJob(cleanup retry) error = %v", err)
	}
	remaining := chunks.byDocument[20]
	if len(remaining) != 1 || remaining[0].GenerationID != "gen-new" {
		t.Fatalf("remaining chunks = %+v", remaining)
	}
	if indexer.cleanedGeneration != "gen-new" || !jobs.completed[31] {
		t.Fatalf("cleanup index/job state: generation=%q completed=%+v", indexer.cleanedGeneration, jobs.completed)
	}
}

func TestGenerationCleanupFailureDoesNotFailDocument(t *testing.T) {
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, IngestionStatus: knowledge.DocumentStatusCompleted, ActiveGenerationID: "gen-new"}}}
	jobs := &fakeJobRepo{next: &knowledge.IngestionJob{BaseModel: domain.BaseModel{ID: 31, OwnerID: 1}, KnowledgeBaseID: 10, DocumentID: 20, JobType: knowledge.IngestionJobTypeGenerationCleanup, MaxAttempts: 3}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendElasticsearch}}},
		docs, &fakeChunkRepo{}, jobs, nil, nil, nil, nil, &fakeIndexer{cleanupErr: errors.New("cleanup failed")}, nil, nil, "",
	)

	processed, err := service.ProcessNext(context.Background(), "worker")
	if !processed || err == nil || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("ProcessNext() = %v, %v", processed, err)
	}
	if docs.items[20].IngestionStatus != knowledge.DocumentStatusCompleted || docs.items[20].IngestionError != "" {
		t.Fatalf("cleanup failure changed document: %+v", docs.items[20])
	}
	if jobs.retrying[31] == "" {
		t.Fatalf("cleanup job was not released for retry: %+v", jobs)
	}
}

type fakeReadStorage struct {
	objects map[string]string
}

func (s fakeReadStorage) Get(_ context.Context, objectKey string) (io.ReadCloser, error) {
	text, ok := s.objects[objectKey]
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(strings.NewReader(text)), nil
}

type fakeIndexer struct {
	ensureCalled      bool
	indexed           []retrieval.ChunkIndexDocument
	deletedByDocument map[int64]int
	indexErr          error
	cleanupErr        error
	cleanedGeneration string
}

func (i *fakeIndexer) EnsureIndex(context.Context) error {
	i.ensureCalled = true
	return nil
}

func (i *fakeIndexer) IndexChunks(_ context.Context, docs []retrieval.ChunkIndexDocument) error {
	if i.indexErr != nil {
		return i.indexErr
	}
	i.indexed = append(i.indexed, docs...)
	return nil
}

func (i *fakeIndexer) DeleteByDocument(_ context.Context, _ int64, documentID int64) error {
	if i.deletedByDocument == nil {
		i.deletedByDocument = make(map[int64]int)
	}
	i.deletedByDocument[documentID]++
	return nil
}

func (i *fakeIndexer) DeleteByKnowledgeBase(context.Context, int64, int64) error {
	return nil
}

func (i *fakeIndexer) SetDocumentEnabled(context.Context, int64, int64, bool) error {
	return nil
}

func (i *fakeIndexer) DeleteInactiveGenerations(_ context.Context, _, _ int64, activeGeneration string) error {
	if i.cleanupErr != nil {
		return i.cleanupErr
	}
	i.cleanedGeneration = activeGeneration
	return nil
}

type fakeKBRepo struct {
	items         map[int64]*knowledge.KnowledgeBase
	documentDelta int
	chunkDelta    int
}

func (r *fakeKBRepo) Create(context.Context, *knowledge.KnowledgeBase) error {
	return nil
}

func (r *fakeKBRepo) ListByOwner(context.Context, int64) ([]knowledge.KnowledgeBase, error) {
	return nil, nil
}

func (r *fakeKBRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeKBRepo) Update(context.Context, *knowledge.KnowledgeBase) error {
	return nil
}

func (r *fakeKBRepo) SoftDelete(context.Context, int64, int64) error {
	return nil
}

func (r *fakeKBRepo) AdjustCounts(_ context.Context, _ int64, _ int64, documentDelta, chunkDelta int) error {
	r.documentDelta += documentDelta
	r.chunkDelta += chunkDelta
	return nil
}

type fakeDocumentRepo struct {
	items map[int64]*knowledge.Document
}

func (r *fakeDocumentRepo) Create(context.Context, *knowledge.Document) error {
	return nil
}

func (r *fakeDocumentRepo) ListByKnowledgeBase(context.Context, int64, int64) ([]knowledge.Document, error) {
	return nil, nil
}

func (r *fakeDocumentRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.Document, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeDocumentRepo) Update(_ context.Context, doc *knowledge.Document) error {
	clone := *doc
	r.items[doc.ID] = &clone
	return nil
}

func (r *fakeDocumentRepo) SetEnabled(_ context.Context, _ int64, id int64, enabled bool) error {
	if doc, ok := r.items[id]; ok {
		doc.Enabled = enabled
	}
	return nil
}

func (r *fakeDocumentRepo) SoftDelete(context.Context, int64, int64) error {
	return nil
}

func (r *fakeDocumentRepo) SoftDeleteByKnowledgeBase(context.Context, int64, int64) error {
	return nil
}

type fakeChunkRepo struct {
	nextID            int64
	byDocument        map[int64][]knowledge.DocumentChunk
	deletedByDocument map[int64]int
	createErr         error
}

func (r *fakeChunkRepo) CreateBatch(_ context.Context, chunks []knowledge.DocumentChunk) error {
	if r.createErr != nil {
		return r.createErr
	}
	if r.byDocument == nil {
		r.byDocument = make(map[int64][]knowledge.DocumentChunk)
	}
	for i := range chunks {
		r.nextID++
		chunks[i].ID = r.nextID
		r.byDocument[chunks[i].DocumentID] = append(r.byDocument[chunks[i].DocumentID], chunks[i])
	}
	return nil
}

func (r *fakeChunkRepo) ListByDocument(_ context.Context, _ int64, documentID int64) ([]knowledge.DocumentChunk, error) {
	return append([]knowledge.DocumentChunk(nil), r.byDocument[documentID]...), nil
}

func (r *fakeChunkRepo) DeleteByDocument(_ context.Context, _ int64, documentID int64) error {
	if r.deletedByDocument == nil {
		r.deletedByDocument = make(map[int64]int)
	}
	r.deletedByDocument[documentID]++
	if r.byDocument != nil {
		delete(r.byDocument, documentID)
	}
	return nil
}

func (r *fakeChunkRepo) DeleteByKnowledgeBase(context.Context, int64, int64) error {
	return nil
}

func (r *fakeChunkRepo) DeleteInactiveGenerations(_ context.Context, _ int64, documentID int64, activeGeneration string) error {
	stored := r.byDocument[documentID]
	remaining := stored[:0]
	for _, chunk := range stored {
		if chunk.GenerationID == activeGeneration {
			remaining = append(remaining, chunk)
		}
	}
	r.byDocument[documentID] = remaining
	return nil
}

type fakeGenerationCommitter struct {
	documents *fakeDocumentRepo
	kbs       *fakeKBRepo
	jobs      *fakeJobRepo
	err       error
	calls     int
}

func (c *fakeGenerationCommitter) Activate(ctx context.Context, doc *knowledge.Document, cleanup *knowledge.IngestionJob, chunkDelta int) error {
	c.calls++
	if c.err != nil {
		return c.err
	}
	if err := c.documents.Update(ctx, doc); err != nil {
		return err
	}
	if err := c.kbs.AdjustCounts(ctx, doc.OwnerID, doc.KnowledgeBaseID, 0, chunkDelta); err != nil {
		return err
	}
	return c.jobs.Create(ctx, cleanup)
}

type fakeJobRepo struct {
	nextID        int64
	next          *knowledge.IngestionJob
	claimed       *knowledge.IngestionJob
	items         map[int64]*knowledge.IngestionJob
	completed     map[int64]bool
	failed        map[int64]string
	retrying      map[int64]string
	markFailedErr error
}

type fakeReliableJobRepo struct {
	*fakeJobRepo
	completedBy string
	failedBy    string
	leaseErr    error
}

func (r *fakeReliableJobRepo) RenewLock(context.Context, int64, string, time.Time) error {
	return r.leaseErr
}

func (r *fakeReliableJobRepo) MarkCompletedOwned(_ context.Context, id int64, workerID string) error {
	if r.leaseErr != nil {
		return r.leaseErr
	}
	r.completedBy = workerID
	return r.MarkCompleted(context.Background(), id)
}

func (r *fakeReliableJobRepo) MarkFailedOwned(ctx context.Context, id int64, workerID, message string) (bool, error) {
	if r.leaseErr != nil {
		return false, r.leaseErr
	}
	r.failedBy = workerID
	return r.MarkFailed(ctx, id, message)
}

var _ knowledge.ReliableIngestionJobRepository = (*fakeReliableJobRepo)(nil)

type fakeProviderRepo struct {
	items map[int64]*providerdomain.ModelProvider
}

func (r *fakeProviderRepo) Create(context.Context, *providerdomain.ModelProvider) error {
	return nil
}

func (r *fakeProviderRepo) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}

func (r *fakeProviderRepo) FindByID(_ context.Context, ownerID, id int64) (*providerdomain.ModelProvider, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeProviderRepo) Update(context.Context, *providerdomain.ModelProvider) error {
	return nil
}

func (r *fakeProviderRepo) SoftDelete(context.Context, int64, int64) error {
	return nil
}

type fakeEmbedder struct {
	vectors [][]float32
	err     error
}

func (e *fakeEmbedder) Embed(context.Context, llm.EmbeddingProviderConfig, llm.EmbeddingRequest) (*llm.EmbeddingResponse, error) {
	if e.err != nil {
		return nil, e.err
	}
	return &llm.EmbeddingResponse{Embeddings: e.vectors}, nil
}

func mustSecretBox(t *testing.T) *cryptoinfra.SecretBox {
	t.Helper()
	box, err := cryptoinfra.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewSecretBox() error = %v", err)
	}
	return box
}

func (r *fakeJobRepo) Create(_ context.Context, job *knowledge.IngestionJob) error {
	r.nextID++
	job.ID = r.nextID
	if r.items == nil {
		r.items = map[int64]*knowledge.IngestionJob{}
	}
	clone := *job
	r.items[job.ID] = &clone
	return nil
}

func (r *fakeJobRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.IngestionJob, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeJobRepo) ClaimNext(context.Context, string) (*knowledge.IngestionJob, error) {
	if r.next == nil {
		return nil, gorm.ErrRecordNotFound
	}
	job := r.next
	job.AttemptCount++
	r.claimed = job
	r.next = nil
	return job, nil
}

func (r *fakeJobRepo) ClaimByID(_ context.Context, ownerID, id int64, workerID string) (*knowledge.IngestionJob, bool, error) {
	item, err := r.FindByID(context.Background(), ownerID, id)
	if err != nil {
		return nil, false, err
	}
	if item.Status == knowledge.IngestionJobStatusCompleted || item.Status == knowledge.IngestionJobStatusFailed || item.Status == knowledge.IngestionJobStatusProcessing {
		return item, false, nil
	}
	item.Status = knowledge.IngestionJobStatusProcessing
	item.AttemptCount++
	item.LockedBy = workerID
	r.items[id] = item
	return item, true, nil
}

func (r *fakeJobRepo) MarkCompleted(_ context.Context, id int64) error {
	if r.completed == nil {
		r.completed = make(map[int64]bool)
	}
	r.completed[id] = true
	return nil
}

func (r *fakeJobRepo) MarkFailed(_ context.Context, id int64, message string) (bool, error) {
	if r.markFailedErr != nil {
		return false, r.markFailedErr
	}
	job := r.claimed
	if job == nil || job.ID != id {
		job = r.next
	}
	maxAttempts := 1
	attemptCount := 0
	if job != nil && job.ID == id {
		job.ErrorMessage = message
		attemptCount = job.AttemptCount
		if job.MaxAttempts > 0 {
			maxAttempts = job.MaxAttempts
		}
	}
	final := attemptCount >= maxAttempts
	if final {
		if r.failed == nil {
			r.failed = make(map[int64]string)
		}
		r.failed[id] = message
	} else {
		if r.retrying == nil {
			r.retrying = make(map[int64]string)
		}
		r.retrying[id] = message
	}
	return final, nil
}

type fakeQueue struct {
	jobs    []queue.Job
	acked   []string
	nacked  []string
	nackErr error
}

func (q *fakeQueue) Publish(context.Context, queue.Job) error {
	return nil
}

func (q *fakeQueue) Claim(context.Context, queue.ClaimOptions) ([]queue.Job, error) {
	if len(q.jobs) == 0 {
		return nil, nil
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return []queue.Job{job}, nil
}

func (q *fakeQueue) Ack(_ context.Context, jobID string) error {
	q.acked = append(q.acked, jobID)
	return nil
}

func (q *fakeQueue) Nack(_ context.Context, jobID string, retryAt time.Time) error {
	q.nacked = append(q.nacked, jobID)
	return q.nackErr
}
