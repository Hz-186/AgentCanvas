package ingestion_usecase

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/parser"

	"gorm.io/gorm"
)

func TestProcessJobParsesChunksIndexesAndCompletes(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				ID:           10,
				OwnerID:      1,
				ChunkSize:    20,
				ChunkOverlap: 0,
			},
		},
	}
	docs := &fakeDocumentRepo{
		items: map[int64]*knowledge.Document{
			20: {
				ID:               20,
				OwnerID:          1,
				KBID:             10,
				Name:             "Guide",
				OriginalFilename: "guide.md",
				FileType:         "md",
				ObjectKey:        "raw/guide.md",
				ParserStatus:     knowledge.DocumentStatusPending,
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
		chunker.NewFixedTokenChunker(),
		indexer,
		nil,
		nil,
		"test_chunks",
	)

	err := service.ProcessJob(ctx, &knowledge.IngestionJob{
		ID:         30,
		OwnerID:    1,
		KBID:       10,
		DocumentID: 20,
	})
	if err != nil {
		t.Fatalf("ProcessJob() error = %v", err)
	}

	doc := docs.items[20]
	if doc.ParserStatus != knowledge.DocumentStatusCompleted {
		t.Fatalf("ParserStatus = %q, want completed", doc.ParserStatus)
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
	for _, chunk := range chunks.byDocument[20] {
		if chunk.ESIndex != "test_chunks" {
			t.Fatalf("ESIndex = %q, want test_chunks", chunk.ESIndex)
		}
		if chunk.ESDocID == "" {
			t.Fatal("ESDocID is empty")
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
				ID:           10,
				OwnerID:      1,
				ChunkSize:    20,
				ChunkOverlap: 0,
			},
		},
	}
	docs := &fakeDocumentRepo{
		items: map[int64]*knowledge.Document{
			20: {
				ID:               20,
				OwnerID:          1,
				KBID:             10,
				Name:             "Guide",
				OriginalFilename: "guide.md",
				FileType:         "md",
				ObjectKey:        "raw/guide.md",
				ParserStatus:     knowledge.DocumentStatusCompleted,
				ChunkCount:       2,
			},
		},
	}
	chunks := &fakeChunkRepo{
		byDocument: map[int64][]knowledge.DocumentChunk{
			20: {
				{ID: 1, OwnerID: 1, KBID: 10, DocumentID: 20, ChunkIndex: 0, Content: "old one"},
				{ID: 2, OwnerID: 1, KBID: 10, DocumentID: 20, ChunkIndex: 1, Content: "old two"},
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
		chunker.NewFixedTokenChunker(),
		indexer,
		nil,
		nil,
		"test_chunks",
	)

	err := service.ProcessJob(ctx, &knowledge.IngestionJob{
		ID:         30,
		OwnerID:    1,
		KBID:       10,
		DocumentID: 20,
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
		10: {ID: 10, OwnerID: 1, ChunkSize: 20},
	}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {
			ID:               20,
			OwnerID:          1,
			KBID:             10,
			OriginalFilename: "guide.md",
			ObjectKey:        "missing.md",
			ParserStatus:     knowledge.DocumentStatusPending,
		},
	}}
	jobs := &fakeJobRepo{
		next: &knowledge.IngestionJob{ID: 30, OwnerID: 1, KBID: 10, DocumentID: 20},
	}
	service := NewService(
		kbs,
		docs,
		&fakeChunkRepo{},
		jobs,
		nil,
		fakeReadStorage{objects: map[string]string{}},
		parser.NewTextParser(),
		chunker.NewFixedTokenChunker(),
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
	if docs.items[20].ParserStatus != knowledge.DocumentStatusFailed {
		t.Fatalf("ParserStatus = %q, want failed", docs.items[20].ParserStatus)
	}
	if docs.items[20].ParserError == "" {
		t.Fatal("ParserError is empty")
	}
}

func TestProcessNextRetriesJobBeforeMaxAttempts(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {ID: 10, OwnerID: 1, ChunkSize: 20},
	}}
	docs := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {
			ID:               20,
			OwnerID:          1,
			KBID:             10,
			OriginalFilename: "guide.md",
			ObjectKey:        "missing.md",
			ParserStatus:     knowledge.DocumentStatusPending,
		},
	}}
	jobs := &fakeJobRepo{
		next: &knowledge.IngestionJob{
			ID:           30,
			OwnerID:      1,
			KBID:         10,
			DocumentID:   20,
			AttemptCount: 1,
			MaxAttempts:  3,
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
		chunker.NewFixedTokenChunker(),
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
	if docs.items[20].ParserStatus == knowledge.DocumentStatusFailed {
		t.Fatal("document was marked failed before max attempts")
	}
	if docs.items[20].ParserError == "" {
		t.Fatal("ParserError is empty")
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
}

func (i *fakeIndexer) EnsureIndex(context.Context) error {
	i.ensureCalled = true
	return nil
}

func (i *fakeIndexer) IndexChunks(_ context.Context, docs []retrieval.ChunkIndexDocument) error {
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
	return item, nil
}

func (r *fakeDocumentRepo) Update(_ context.Context, doc *knowledge.Document) error {
	r.items[doc.ID] = doc
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
}

func (r *fakeChunkRepo) CreateBatch(_ context.Context, chunks []knowledge.DocumentChunk) error {
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

func (r *fakeChunkRepo) UpdateIndexRefs(_ context.Context, chunks []knowledge.DocumentChunk) error {
	if r.byDocument == nil {
		return nil
	}
	for _, incoming := range chunks {
		stored := r.byDocument[incoming.DocumentID]
		for i := range stored {
			if stored[i].ID == incoming.ID {
				stored[i].ESIndex = incoming.ESIndex
				stored[i].ESDocID = incoming.ESDocID
			}
		}
		r.byDocument[incoming.DocumentID] = stored
	}
	return nil
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

type fakeJobRepo struct {
	next      *knowledge.IngestionJob
	claimed   *knowledge.IngestionJob
	completed map[int64]bool
	failed    map[int64]string
	retrying  map[int64]string
}

func (r *fakeJobRepo) Create(context.Context, *knowledge.IngestionJob) error {
	return nil
}

func (r *fakeJobRepo) FindByID(context.Context, int64, int64) (*knowledge.IngestionJob, error) {
	return nil, nil
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

func (r *fakeJobRepo) MarkCompleted(_ context.Context, id int64) error {
	if r.completed == nil {
		r.completed = make(map[int64]bool)
	}
	r.completed[id] = true
	return nil
}

func (r *fakeJobRepo) MarkFailed(_ context.Context, id int64, message string) (bool, error) {
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
