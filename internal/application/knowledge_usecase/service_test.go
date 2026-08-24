package knowledge_usecase

import (
	"agentcanvas/internal/domain"
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"testing"
	"time"

	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/queue"
	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

func TestUploadDocumentStoresFileCreatesDocumentAndJob(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}},
				ChunkSize:       800,
				ChunkOverlap:    100,
			},
		},
	}
	documents := &fakeDocumentRepo{}
	jobs := &fakeJobRepo{}
	storage := &fakeWriteStorage{objects: map[string]string{}}
	service := NewService(
		kbs,
		documents,
		&fakeChunkRepo{},
		jobs,
		&fakeRetrievalLogRepo{},
		nil,
		storage,
		&fakeRetriever{},
		&fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "guide.md", "text/markdown", "# Intro\nAgentCanvas supports txt and md.")
	resp, err := service.UploadDocument(ctx, 1, 10, UploadDocumentRequest{
		FileHeader:  header,
		ContentType: "text/markdown",
	}, ClientInfo{})
	if err != nil {
		t.Fatalf("UploadDocument() error = %v", err)
	}

	if resp.Document.ID == 0 {
		t.Fatal("document ID was not assigned")
	}
	if resp.Document.IngestionStatus != knowledge.DocumentStatusPending {
		t.Fatalf("ParserStatus = %q, want pending", resp.Document.IngestionStatus)
	}
	if resp.Document.FileType != "md" {
		t.Fatalf("FileType = %q, want md", resp.Document.FileType)
	}
	if resp.Document.StorageObjectKey == "" {
		t.Fatal("ObjectKey is empty")
	}
	if resp.Document.ContentHash == "" {
		t.Fatal("ContentHash is empty")
	}
	if got := storage.objects[resp.Document.StorageObjectKey]; got != "# Intro\nAgentCanvas supports txt and md." {
		t.Fatalf("stored object = %q", got)
	}
	if resp.Job.ID == 0 {
		t.Fatal("job ID was not assigned")
	}
	if resp.Job.Status != knowledge.IngestionJobStatusPending {
		t.Fatalf("job status = %q, want pending", resp.Job.Status)
	}
	if kbs.documentDelta != 1 {
		t.Fatalf("document delta = %d, want 1", kbs.documentDelta)
	}
}

func TestUploadDocumentPublishesConfiguredJobQueue(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, ChunkSize: 800, ChunkOverlap: 100}}}
	documents := &fakeDocumentRepo{}
	jobs := &fakeJobRepo{}
	jobQueue := &fakeQueue{}
	service := NewService(
		kbs,
		documents,
		&fakeChunkRepo{},
		jobs,
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{objects: map[string]string{}},
		&fakeRetriever{},
		&fakeIndexer{},
	).WithJobQueue(jobQueue)

	header := mustMultipartFileHeader(t, "guide.md", "text/markdown", "# Intro")
	resp, err := service.UploadDocument(ctx, 1, 10, UploadDocumentRequest{FileHeader: header, ContentType: "text/markdown"}, ClientInfo{})
	if err != nil {
		t.Fatalf("UploadDocument() error = %v", err)
	}
	if len(jobQueue.published) != 1 {
		t.Fatalf("expected one published queue job, got %+v", jobQueue.published)
	}
	published := jobQueue.published[0]
	if published.ID != "1" || published.Type != knowledge.IngestionJobTypeDocument || published.Payload["ingestion_job_id"] != resp.Job.ID || published.Payload["document_id"] != resp.Document.ID {
		t.Fatalf("unexpected published queue job: %+v", published)
	}
}

func TestCreateKnowledgeBaseDefaultsToRecursiveChunkMethod(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{}
	service := NewService(kbs, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})

	kb, err := service.CreateKnowledgeBase(ctx, 1, CreateKnowledgeBaseRequest{Name: "Docs"}, ClientInfo{})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase() error = %v", err)
	}
	if kb.ChunkMethod != knowledge.ChunkMethodRecursive || kb.EmbeddingMetric != knowledge.EmbeddingMetricCosine {
		t.Fatalf("unexpected defaults: %+v", kb)
	}
}

func TestCreateKnowledgeBaseRejectsBackendMetricMismatch(t *testing.T) {
	providerID := int64(9)
	service := NewService(&fakeKBRepo{}, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})
	_, err := service.CreateKnowledgeBase(context.Background(), 1, CreateKnowledgeBaseRequest{
		Name: "Vector Docs", RetrievalMode: knowledge.RetrievalModeVector, EmbeddingProviderID: &providerID, EmbeddingMetric: knowledge.EmbeddingMetricIP,
	}, ClientInfo{})
	if err == nil {
		t.Fatal("CreateKnowledgeBase() accepted a metric that the configured backend cannot execute")
	}
}

func TestCreateKnowledgeBaseRejectsUnsupportedChunkMethod(t *testing.T) {
	service := NewService(&fakeKBRepo{}, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})

	_, err := service.CreateKnowledgeBase(context.Background(), 1, CreateKnowledgeBaseRequest{Name: "Docs", ChunkMethod: "semantic"}, ClientInfo{})
	if err == nil {
		t.Fatal("CreateKnowledgeBase() error = nil, want invalid input")
	}
}

func TestCreateKnowledgeBaseRequiresExperimentalPythonChunkingFlag(t *testing.T) {
	service := NewService(&fakeKBRepo{}, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})
	if _, err := service.CreateKnowledgeBase(context.Background(), 1, CreateKnowledgeBaseRequest{Name: "Docs", ChunkMethod: "python:recursive"}, ClientInfo{}); err == nil {
		t.Fatal("Python chunking should remain disabled before benchmark approval")
	}
	service.ConfigurePythonChunking(true)
	kb, err := service.CreateKnowledgeBase(context.Background(), 1, CreateKnowledgeBaseRequest{Name: "Docs", ChunkMethod: "python:recursive"}, ClientInfo{})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase() error = %v", err)
	}
	if kb.ChunkMethod != "python:recursive" {
		t.Fatalf("ChunkMethod = %q, want python:recursive", kb.ChunkMethod)
	}
	service.ConfigurePythonChunking(true, "python:langchain_recursive")
	kb, err = service.CreateKnowledgeBase(context.Background(), 1, CreateKnowledgeBaseRequest{Name: "LangChain", ChunkMethod: "python:langchain_recursive"}, ClientInfo{})
	if err != nil || kb.ChunkMethod != "python:langchain_recursive" {
		t.Fatalf("LangChain chunk method = %q error=%v", kb.ChunkMethod, err)
	}
	if _, err := service.CreateKnowledgeBase(context.Background(), 1, CreateKnowledgeBaseRequest{Name: "Blocked", ChunkMethod: "python:recursive"}, ClientInfo{}); err == nil {
		t.Fatal("Python method outside the configured allowlist was accepted")
	}
}

func TestUploadDocumentRejectsUnsupportedFileType(t *testing.T) {
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		&fakeDocumentRepo{},
		&fakeChunkRepo{},
		&fakeJobRepo{},
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{},
		&fakeRetriever{},
		&fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "paper.exe", "application/octet-stream", "exe")
	if _, err := service.UploadDocument(context.Background(), 1, 10, UploadDocumentRequest{FileHeader: header}, ClientInfo{}); err == nil {
		t.Fatal("UploadDocument() error = nil, want invalid input")
	}
}

func TestUploadDocumentMarksDocumentFailedWhenStorageFails(t *testing.T) {
	ctx := context.Background()
	documents := &fakeDocumentRepo{}
	jobs := &fakeJobRepo{}
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}}
	service := NewService(
		kbs,
		documents,
		&fakeChunkRepo{},
		jobs,
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{err: errors.New("storage unavailable")},
		&fakeRetriever{},
		&fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "guide.md", "text/markdown", "# Intro")
	if _, err := service.UploadDocument(ctx, 1, 10, UploadDocumentRequest{FileHeader: header}, ClientInfo{}); err == nil {
		t.Fatal("UploadDocument() error = nil, want storage error")
	}
	doc := documents.items[1]
	if doc == nil {
		t.Fatal("document was not created")
	}
	if doc.IngestionStatus != knowledge.DocumentStatusFailed {
		t.Fatalf("ParserStatus = %q, want failed", doc.IngestionStatus)
	}
	if doc.IngestionError == "" {
		t.Fatal("ParserError is empty")
	}
	if len(jobs.items) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs.items))
	}
	if kbs.documentDelta != 0 {
		t.Fatalf("document delta = %d, want 0", kbs.documentDelta)
	}
}

func TestUploadDocumentReturnsFailureStatePersistenceErrors(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	updateErr := errors.New("document update unavailable")
	documents := &fakeDocumentRepo{updateErr: updateErr, updateErrAt: 1}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		documents, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil,
		&fakeWriteStorage{err: storageErr}, &fakeRetriever{}, &fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "guide.md", "text/markdown", "# Intro")
	_, err := service.UploadDocument(context.Background(), 1, 10, UploadDocumentRequest{FileHeader: header}, ClientInfo{})
	if !errors.Is(err, storageErr) || !errors.Is(err, updateErr) {
		t.Fatalf("UploadDocument() error = %v, want storage and persistence errors", err)
	}
}

func TestUploadDocumentMarksDocumentFailedWhenOpenFails(t *testing.T) {
	documents := &fakeDocumentRepo{}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		documents, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil,
		&fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{},
	)

	_, err := service.UploadDocument(context.Background(), 1, 10, UploadDocumentRequest{
		FileHeader: &multipart.FileHeader{Filename: "guide.md", Size: 1},
	}, ClientInfo{})
	if err == nil || documents.items[1].IngestionStatus != knowledge.DocumentStatusFailed || documents.items[1].IngestionError == "" {
		t.Fatalf("open failure was not persisted: document=%+v error=%v", documents.items[1], err)
	}
}

func TestUploadDocumentMarksDocumentFailedWhenJobCreateFails(t *testing.T) {
	ctx := context.Background()
	documents := &fakeDocumentRepo{}
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}}
	service := NewService(
		kbs,
		documents,
		&fakeChunkRepo{},
		&fakeJobRepo{createErr: errors.New("job table unavailable")},
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{objects: map[string]string{}},
		&fakeRetriever{},
		&fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "guide.md", "text/markdown", "# Intro")
	if _, err := service.UploadDocument(ctx, 1, 10, UploadDocumentRequest{FileHeader: header}, ClientInfo{}); err == nil {
		t.Fatal("UploadDocument() error = nil, want job create error")
	}
	doc := documents.items[1]
	if doc == nil {
		t.Fatal("document was not created")
	}
	if doc.IngestionStatus != knowledge.DocumentStatusFailed {
		t.Fatalf("ParserStatus = %q, want failed", doc.IngestionStatus)
	}
	if doc.IngestionError == "" {
		t.Fatal("ParserError is empty")
	}
	if kbs.documentDelta != 0 {
		t.Fatalf("document delta = %d, want 0", kbs.documentDelta)
	}
}

func TestUploadDocumentReturnsJobAndFailureStatePersistenceErrors(t *testing.T) {
	jobErr := errors.New("job table unavailable")
	updateErr := errors.New("document update unavailable")
	documents := &fakeDocumentRepo{updateErr: updateErr, updateErrAt: 2}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		documents, &fakeChunkRepo{}, &fakeJobRepo{createErr: jobErr}, &fakeRetrievalLogRepo{}, nil,
		&fakeWriteStorage{objects: map[string]string{}}, &fakeRetriever{}, &fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "guide.md", "text/markdown", "# Intro")
	_, err := service.UploadDocument(context.Background(), 1, 10, UploadDocumentRequest{FileHeader: header}, ClientInfo{})
	if !errors.Is(err, jobErr) || !errors.Is(err, updateErr) {
		t.Fatalf("UploadDocument() error = %v, want job and persistence errors", err)
	}
}

func TestSearchCallsRetrieverAndWritesRetrievalLog(t *testing.T) {
	ctx := context.Background()
	logs := &fakeRetrievalLogRepo{}
	retriever := &fakeRetriever{
		response: &retrieval.RetrievalResponse{
			Results: []retrieval.RetrievalResult{
				{
					ChunkID:         100,
					DocumentID:      20,
					KnowledgeBaseID: 10,
					Score:           1.25,
					Content:         "AgentCanvas knowledge retrieval",
					Highlight:       "<em>AgentCanvas</em> knowledge retrieval",
					DocumentName:    "guide.md",
				},
			},
			LatencyMS: 12,
		},
	}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		&fakeDocumentRepo{},
		&fakeChunkRepo{},
		&fakeJobRepo{},
		logs,
		nil,
		&fakeWriteStorage{},
		retriever,
		&fakeIndexer{},
	).ConfigureRetrievalBackend(knowledge.RetrievalBackendMilvus)

	resp, err := service.Search(ctx, 1, 10, SearchRequest{Query: " AgentCanvas ", TopK: 5})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	if retriever.request.OwnerID != 1 || len(retriever.request.KnowledgeBaseIDs) != 1 || retriever.request.KnowledgeBaseIDs[0] != 10 {
		t.Fatalf("retriever request = %#v", retriever.request)
	}
	if retriever.request.Query != "AgentCanvas" {
		t.Fatalf("retriever query = %q, want trimmed query", retriever.request.Query)
	}
	if retriever.request.EmbeddingProfile != "" {
		t.Fatalf("keyword retrieval must not filter by embedding profile: %q", retriever.request.EmbeddingProfile)
	}
	if len(logs.items) != 1 {
		t.Fatalf("retrieval logs = %d, want 1", len(logs.items))
	}
	if logs.items[0].ResultCount != 1 || logs.items[0].LatencyMS != 12 || logs.items[0].RetrievalBackend != knowledge.RetrievalBackendMilvus {
		t.Fatalf("retrieval log = %#v", logs.items[0])
	}
}

func TestSearchDispatchesRetrieverAndLogsKnowledgeBaseBackend(t *testing.T) {
	logs := &fakeRetrievalLogRepo{}
	defaultRetriever := &fakeRetriever{}
	milvusRetriever := &fakeRetriever{}
	documents := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, ActiveGenerationID: "gen-active"}}}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendMilvus}}},
		documents, &fakeChunkRepo{}, &fakeJobRepo{}, logs, nil, &fakeWriteStorage{}, defaultRetriever, &fakeIndexer{},
	).ConfigureRetrievalBackends(map[string]retrieval.Retriever{
		knowledge.RetrievalBackendMilvus: milvusRetriever,
	}, nil)

	if _, err := service.Search(context.Background(), 1, 10, SearchRequest{Query: "AgentCanvas"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !milvusRetriever.called || defaultRetriever.called {
		t.Fatalf("retriever dispatch: default=%v milvus=%v", defaultRetriever.called, milvusRetriever.called)
	}
	if milvusRetriever.request.ActiveGenerationIDs[20] != "gen-active" {
		t.Fatalf("active generations = %+v", milvusRetriever.request.ActiveGenerationIDs)
	}
	if len(logs.items) != 1 || logs.items[0].RetrievalBackend != knowledge.RetrievalBackendMilvus {
		t.Fatalf("retrieval logs = %+v", logs.items)
	}
}

func TestSearchRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		req  SearchRequest
	}{
		{name: "blank query", req: SearchRequest{Query: "   "}},
		{name: "negative top k", req: SearchRequest{Query: "AgentCanvas", TopK: -1}},
		{name: "too large top k", req: SearchRequest{Query: "AgentCanvas", TopK: 51}},
		{name: "unsupported mode", req: SearchRequest{Query: "AgentCanvas", Mode: retrieval.Mode("semantic")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retriever := &fakeRetriever{}
			service := NewService(
				&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
				&fakeDocumentRepo{},
				&fakeChunkRepo{},
				&fakeJobRepo{},
				&fakeRetrievalLogRepo{},
				nil,
				&fakeWriteStorage{},
				retriever,
				&fakeIndexer{},
			)

			if _, err := service.Search(ctx, 1, 10, tc.req); err == nil {
				t.Fatal("Search() error = nil, want invalid input")
			}
			if retriever.called {
				t.Fatal("retriever was called for invalid request")
			}
		})
	}
}

func TestSetDocumentEnabledTogglesAndSyncsIndex(t *testing.T) {
	ctx := context.Background()
	documents := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Enabled: true},
	}}
	indexer := &fakeIndexer{}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		documents,
		&fakeChunkRepo{},
		&fakeJobRepo{},
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{},
		&fakeRetriever{},
		indexer,
	)

	doc, err := service.SetDocumentEnabled(ctx, 1, 20, false, ClientInfo{})
	if err != nil {
		t.Fatalf("SetDocumentEnabled() error = %v", err)
	}
	if doc.Enabled {
		t.Fatal("returned document should be disabled")
	}
	if documents.items[20].Enabled {
		t.Fatal("repository document should be disabled")
	}
	if len(indexer.enabledCalls) != 1 || indexer.enabledCalls[0].documentID != 20 || indexer.enabledCalls[0].enabled {
		t.Fatalf("index sync calls = %#v", indexer.enabledCalls)
	}
}

func TestSetDocumentEnabledIsNoopWhenUnchanged(t *testing.T) {
	ctx := context.Background()
	documents := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Enabled: true},
	}}
	indexer := &fakeIndexer{}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		documents,
		&fakeChunkRepo{},
		&fakeJobRepo{},
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{},
		&fakeRetriever{},
		indexer,
	)

	if _, err := service.SetDocumentEnabled(ctx, 1, 20, true, ClientInfo{}); err != nil {
		t.Fatalf("SetDocumentEnabled() error = %v", err)
	}
	if len(indexer.enabledCalls) != 0 {
		t.Fatalf("index should not be touched when state is unchanged: %#v", indexer.enabledCalls)
	}
}

func TestSetDocumentEnabledReturnsIndexAndRollbackErrors(t *testing.T) {
	indexErr := errors.New("index unavailable")
	rollbackErr := errors.New("rollback unavailable")
	documents := &fakeDocumentRepo{items: map[int64]*knowledge.Document{
		20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10, Enabled: true},
	}, setEnabledErr: rollbackErr, setEnabledErrAt: 2}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}}}},
		documents, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil,
		&fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{setEnabledErr: indexErr},
	)

	_, err := service.SetDocumentEnabled(context.Background(), 1, 20, false, ClientInfo{})
	if !errors.Is(err, indexErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("SetDocumentEnabled() error = %v, want index and rollback errors", err)
	}
}

func TestUpdateKnowledgeBaseResetsDimensionsWhenEmbeddingChanges(t *testing.T) {
	ctx := context.Background()
	providerID := int64(5)
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalMode: "keyword", EmbeddingProviderID: &providerID, EmbeddingDimensions: 1024, ChunkSize: 800, ChunkOverlap: 100},
	}}
	service := NewService(kbs, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})

	mode := "vector"
	kb, err := service.UpdateKnowledgeBase(ctx, 1, 10, UpdateKnowledgeBaseRequest{RetrievalMode: &mode}, ClientInfo{})
	if err != nil {
		t.Fatalf("UpdateKnowledgeBase() error = %v", err)
	}
	if kb.EmbeddingDimensions != 0 {
		t.Fatalf("EmbeddingDimensions = %d, want 0 after embedding config change", kb.EmbeddingDimensions)
	}
}

func TestUpdateKnowledgeBaseKeepsDimensionsWhenEmbeddingUnchanged(t *testing.T) {
	ctx := context.Background()
	providerID := int64(5)
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalMode: "vector", EmbeddingProviderID: &providerID, EmbeddingDimensions: 1024, ChunkSize: 800, ChunkOverlap: 100},
	}}
	service := NewService(kbs, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})

	desc := "updated description"
	kb, err := service.UpdateKnowledgeBase(ctx, 1, 10, UpdateKnowledgeBaseRequest{Description: &desc}, ClientInfo{})
	if err != nil {
		t.Fatalf("UpdateKnowledgeBase() error = %v", err)
	}
	if kb.EmbeddingDimensions != 1024 {
		t.Fatalf("EmbeddingDimensions = %d, want 1024 preserved", kb.EmbeddingDimensions)
	}
}

func TestUpdateKnowledgeBaseRejectsIndexedVectorSpaceChange(t *testing.T) {
	providerID := int64(5)
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalBackend: knowledge.RetrievalBackendElasticsearch, RetrievalMode: knowledge.RetrievalModeVector,
			EmbeddingProviderID: &providerID, EmbeddingModel: "model-a", EmbeddingDimensions: 2, EmbeddingMetric: knowledge.EmbeddingMetricCosine,
			ChunkSize: 800, ChunkOverlap: 100},
	}}
	documents := &fakeDocumentRepo{items: map[int64]*knowledge.Document{20: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 1}}, KnowledgeBaseID: 10}}}
	service := NewService(kbs, documents, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})
	model := "model-b"

	_, err := service.UpdateKnowledgeBase(context.Background(), 1, 10, UpdateKnowledgeBaseRequest{EmbeddingModel: &model}, ClientInfo{})
	if !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("indexed vector-space change error = %v, want conflict", err)
	}
}

func TestUpdateKnowledgeBaseRejectsVectorWithoutProvider(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalMode: "keyword", ChunkSize: 800, ChunkOverlap: 100},
	}}
	service := NewService(kbs, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})

	mode := "vector"
	if _, err := service.UpdateKnowledgeBase(ctx, 1, 10, UpdateKnowledgeBaseRequest{RetrievalMode: &mode}, ClientInfo{}); err == nil {
		t.Fatal("UpdateKnowledgeBase() error = nil, want invalid input for vector without provider")
	}
}

func TestUpdateKnowledgeBaseUpdatesChunkMethod(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{
		10: {SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 10, OwnerID: 1}}, RetrievalMode: "keyword", ChunkMethod: knowledge.ChunkMethodRecursive, ChunkSize: 800, ChunkOverlap: 100},
	}}
	service := NewService(kbs, &fakeDocumentRepo{}, &fakeChunkRepo{}, &fakeJobRepo{}, &fakeRetrievalLogRepo{}, nil, &fakeWriteStorage{}, &fakeRetriever{}, &fakeIndexer{})

	method := knowledge.ChunkMethodFixedToken
	kb, err := service.UpdateKnowledgeBase(ctx, 1, 10, UpdateKnowledgeBaseRequest{ChunkMethod: &method}, ClientInfo{})
	if err != nil {
		t.Fatalf("UpdateKnowledgeBase() error = %v", err)
	}
	if kb.ChunkMethod != knowledge.ChunkMethodFixedToken {
		t.Fatalf("ChunkMethod = %q, want fixed_token", kb.ChunkMethod)
	}
}

func mustMultipartFileHeader(t *testing.T, filename, contentType, content string) *multipart.FileHeader {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart() error = %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	reader := multipart.NewReader(&body, writer.Boundary())
	form, err := reader.ReadForm(1024 * 1024)
	if err != nil {
		t.Fatalf("ReadForm() error = %v", err)
	}
	files := form.File["file"]
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	return files[0]
}

type fakeWriteStorage struct {
	objects map[string]string
	err     error
}

func (s *fakeWriteStorage) Put(_ context.Context, objectKey string, reader io.Reader, _ int64, _ string) error {
	if s.err != nil {
		return s.err
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if s.objects == nil {
		s.objects = make(map[string]string)
	}
	s.objects[objectKey] = string(data)
	return nil
}

type fakeRetriever struct {
	request  retrieval.RetrievalRequest
	response *retrieval.RetrievalResponse
	called   bool
}

func (r *fakeRetriever) Search(_ context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	r.called = true
	r.request = req
	if r.response != nil {
		return r.response, nil
	}
	return &retrieval.RetrievalResponse{}, nil
}

type fakeIndexer struct {
	enabledCalls  []enabledCall
	setEnabledErr error
}

type fakeQueue struct {
	published []queue.Job
}

func (q *fakeQueue) Publish(_ context.Context, job queue.Job) error {
	q.published = append(q.published, job)
	return nil
}

func (q *fakeQueue) Claim(context.Context, queue.ClaimOptions) ([]queue.Job, error) {
	return nil, nil
}

func (q *fakeQueue) Ack(context.Context, string) error {
	return nil
}

func (q *fakeQueue) Nack(context.Context, string, time.Time) error {
	return nil
}

type enabledCall struct {
	documentID int64
	enabled    bool
}

func (i *fakeIndexer) EnsureIndex(context.Context) error {
	return nil
}

func (i *fakeIndexer) IndexChunks(context.Context, []retrieval.ChunkIndexDocument) error {
	return nil
}

func (i *fakeIndexer) DeleteByDocument(context.Context, int64, int64) error {
	return nil
}

func (i *fakeIndexer) DeleteByKnowledgeBase(context.Context, int64, int64) error {
	return nil
}

func (i *fakeIndexer) SetDocumentEnabled(_ context.Context, _ int64, documentID int64, enabled bool) error {
	i.enabledCalls = append(i.enabledCalls, enabledCall{documentID: documentID, enabled: enabled})
	return i.setEnabledErr
}

type fakeRetrievalLogRepo struct {
	items []knowledge.RetrievalLog
}

func (r *fakeRetrievalLogRepo) Create(_ context.Context, log *knowledge.RetrievalLog) error {
	r.items = append(r.items, *log)
	return nil
}

type fakeKBRepo struct {
	items         map[int64]*knowledge.KnowledgeBase
	documentDelta int
	chunkDelta    int
}

func (r *fakeKBRepo) Create(_ context.Context, kb *knowledge.KnowledgeBase) error {
	kb.ID = int64(len(r.items) + 1)
	if r.items == nil {
		r.items = make(map[int64]*knowledge.KnowledgeBase)
	}
	clone := *kb
	r.items[kb.ID] = &clone
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
	nextID          int64
	items           map[int64]*knowledge.Document
	updateCalls     int
	updateErr       error
	updateErrAt     int
	setEnabledCalls int
	setEnabledErr   error
	setEnabledErrAt int
}

func (r *fakeDocumentRepo) Create(_ context.Context, doc *knowledge.Document) error {
	r.nextID++
	doc.ID = r.nextID
	if r.items == nil {
		r.items = make(map[int64]*knowledge.Document)
	}
	clone := *doc
	r.items[doc.ID] = &clone
	return nil
}

func (r *fakeDocumentRepo) ListByKnowledgeBase(_ context.Context, ownerID, kbID int64) ([]knowledge.Document, error) {
	items := make([]knowledge.Document, 0)
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.KnowledgeBaseID == kbID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeDocumentRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.Document, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

func (r *fakeDocumentRepo) Update(_ context.Context, doc *knowledge.Document) error {
	r.updateCalls++
	if r.updateErr != nil && (r.updateErrAt == 0 || r.updateCalls == r.updateErrAt) {
		return r.updateErr
	}
	if r.items == nil {
		r.items = make(map[int64]*knowledge.Document)
	}
	clone := *doc
	r.items[doc.ID] = &clone
	return nil
}

func (r *fakeDocumentRepo) SetEnabled(_ context.Context, ownerID, id int64, enabled bool) error {
	r.setEnabledCalls++
	if r.setEnabledErr != nil && (r.setEnabledErrAt == 0 || r.setEnabledCalls == r.setEnabledErrAt) {
		return r.setEnabledErr
	}
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return gorm.ErrRecordNotFound
	}
	item.Enabled = enabled
	return nil
}

func (r *fakeDocumentRepo) SoftDelete(context.Context, int64, int64) error {
	return nil
}

func (r *fakeDocumentRepo) SoftDeleteByKnowledgeBase(context.Context, int64, int64) error {
	return nil
}

type fakeChunkRepo struct{}

func (r *fakeChunkRepo) CreateBatch(context.Context, []knowledge.DocumentChunk) error {
	return nil
}

func (r *fakeChunkRepo) ListByDocument(context.Context, int64, int64) ([]knowledge.DocumentChunk, error) {
	return nil, nil
}

func (r *fakeChunkRepo) UpdateIndexRefs(context.Context, []knowledge.DocumentChunk) error {
	return nil
}

func (r *fakeChunkRepo) DeleteByDocument(context.Context, int64, int64) error {
	return nil
}

func (r *fakeChunkRepo) DeleteByKnowledgeBase(context.Context, int64, int64) error {
	return nil
}

type fakeJobRepo struct {
	nextID    int64
	items     map[int64]*knowledge.IngestionJob
	createErr error
}

func (r *fakeJobRepo) Create(_ context.Context, job *knowledge.IngestionJob) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.nextID++
	job.ID = r.nextID
	if r.items == nil {
		r.items = make(map[int64]*knowledge.IngestionJob)
	}
	clone := *job
	r.items[job.ID] = &clone
	return nil
}

func (r *fakeJobRepo) FindByID(context.Context, int64, int64) (*knowledge.IngestionJob, error) {
	return nil, nil
}

func (r *fakeJobRepo) ClaimNext(context.Context, string) (*knowledge.IngestionJob, error) {
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeJobRepo) ClaimByID(context.Context, int64, int64, string) (*knowledge.IngestionJob, bool, error) {
	return nil, false, gorm.ErrRecordNotFound
}

func (r *fakeJobRepo) MarkCompleted(context.Context, int64) error {
	return nil
}

func (r *fakeJobRepo) MarkFailed(context.Context, int64, string) (bool, error) {
	return true, nil
}
