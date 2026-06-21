package knowledge_usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"testing"

	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/retrieval"

	"gorm.io/gorm"
)

func TestUploadDocumentStoresFileCreatesDocumentAndJob(t *testing.T) {
	ctx := context.Background()
	kbs := &fakeKBRepo{
		items: map[int64]*knowledge.KnowledgeBase{
			10: {
				ID:           10,
				OwnerID:      1,
				ChunkSize:    800,
				ChunkOverlap: 100,
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
	if resp.Document.ParserStatus != knowledge.DocumentStatusPending {
		t.Fatalf("ParserStatus = %q, want pending", resp.Document.ParserStatus)
	}
	if resp.Document.FileType != "md" {
		t.Fatalf("FileType = %q, want md", resp.Document.FileType)
	}
	if resp.Document.ObjectKey == "" {
		t.Fatal("ObjectKey is empty")
	}
	if resp.Document.ContentHash == "" {
		t.Fatal("ContentHash is empty")
	}
	if got := storage.objects[resp.Document.ObjectKey]; got != "# Intro\nAgentCanvas supports txt and md." {
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

func TestUploadDocumentRejectsUnsupportedFileType(t *testing.T) {
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1}}},
		&fakeDocumentRepo{},
		&fakeChunkRepo{},
		&fakeJobRepo{},
		&fakeRetrievalLogRepo{},
		nil,
		&fakeWriteStorage{},
		&fakeRetriever{},
		&fakeIndexer{},
	)

	header := mustMultipartFileHeader(t, "paper.pdf", "application/pdf", "pdf")
	if _, err := service.UploadDocument(context.Background(), 1, 10, UploadDocumentRequest{FileHeader: header}, ClientInfo{}); err == nil {
		t.Fatal("UploadDocument() error = nil, want invalid input")
	}
}

func TestUploadDocumentMarksDocumentFailedWhenStorageFails(t *testing.T) {
	ctx := context.Background()
	documents := &fakeDocumentRepo{}
	jobs := &fakeJobRepo{}
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1}}}
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
	if doc.ParserStatus != knowledge.DocumentStatusFailed {
		t.Fatalf("ParserStatus = %q, want failed", doc.ParserStatus)
	}
	if doc.ParserError == "" {
		t.Fatal("ParserError is empty")
	}
	if len(jobs.items) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs.items))
	}
	if kbs.documentDelta != 0 {
		t.Fatalf("document delta = %d, want 0", kbs.documentDelta)
	}
}

func TestUploadDocumentMarksDocumentFailedWhenJobCreateFails(t *testing.T) {
	ctx := context.Background()
	documents := &fakeDocumentRepo{}
	kbs := &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1}}}
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
	if doc.ParserStatus != knowledge.DocumentStatusFailed {
		t.Fatalf("ParserStatus = %q, want failed", doc.ParserStatus)
	}
	if doc.ParserError == "" {
		t.Fatal("ParserError is empty")
	}
	if kbs.documentDelta != 0 {
		t.Fatalf("document delta = %d, want 0", kbs.documentDelta)
	}
}

func TestSearchCallsRetrieverAndWritesRetrievalLog(t *testing.T) {
	ctx := context.Background()
	logs := &fakeRetrievalLogRepo{}
	retriever := &fakeRetriever{
		response: &retrieval.RetrievalResponse{
			Results: []retrieval.RetrievalResult{
				{
					ChunkID:      100,
					DocumentID:   20,
					KBID:         10,
					Score:        1.25,
					Content:      "AgentCanvas knowledge retrieval",
					Highlight:    "<em>AgentCanvas</em> knowledge retrieval",
					DocumentName: "guide.md",
				},
			},
			LatencyMS: 12,
		},
	}
	service := NewService(
		&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1}}},
		&fakeDocumentRepo{},
		&fakeChunkRepo{},
		&fakeJobRepo{},
		logs,
		nil,
		&fakeWriteStorage{},
		retriever,
		&fakeIndexer{},
	)

	resp, err := service.Search(ctx, 1, 10, SearchRequest{Query: " AgentCanvas ", TopK: 5})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	if retriever.request.OwnerID != 1 || len(retriever.request.KBIDs) != 1 || retriever.request.KBIDs[0] != 10 {
		t.Fatalf("retriever request = %#v", retriever.request)
	}
	if retriever.request.Query != "AgentCanvas" {
		t.Fatalf("retriever query = %q, want trimmed query", retriever.request.Query)
	}
	if len(logs.items) != 1 {
		t.Fatalf("retrieval logs = %d, want 1", len(logs.items))
	}
	if logs.items[0].ResultCount != 1 || logs.items[0].LatencyMS != 12 {
		t.Fatalf("retrieval log = %#v", logs.items[0])
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
				&fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{10: {ID: 10, OwnerID: 1}}},
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

type fakeIndexer struct{}

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
	nextID int64
	items  map[int64]*knowledge.Document
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
	if r.items == nil {
		r.items = make(map[int64]*knowledge.Document)
	}
	clone := *doc
	r.items[doc.ID] = &clone
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

func (r *fakeJobRepo) MarkCompleted(context.Context, int64) error {
	return nil
}

func (r *fakeJobRepo) MarkFailed(context.Context, int64, string) (bool, error) {
	return true, nil
}
