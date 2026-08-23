package knowledge_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strconv"
	"strings"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/knowledge"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/queue"
	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type FileStorage interface {
	Put(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error
}

type Service struct {
	kbs                   knowledge.KnowledgeBaseRepository
	documents             knowledge.DocumentRepository
	chunks                knowledge.ChunkRepository
	jobs                  knowledge.IngestionJobRepository
	logs                  knowledge.RetrievalLogRepository
	audits                audit.Repository
	storage               FileStorage
	retriever             retrieval.Retriever
	indexer               retrieval.Indexer
	retrievers            map[string]retrieval.Retriever
	indexers              map[string]retrieval.Indexer
	jobQueue              queue.JobQueue
	pythonChunkingEnabled bool
	pythonChunkMethods    map[string]struct{}
	retrievalBackend      string
	embeddingMetric       string
}

type ClientInfo struct {
	UserAgent string
	IPAddress string
}

func (s *Service) WithJobQueue(jobQueue queue.JobQueue) *Service {
	s.jobQueue = jobQueue
	return s
}

func (s *Service) ConfigureRetrievalBackend(backend string) *Service {
	backend = strings.TrimSpace(backend)
	if backend == knowledge.RetrievalBackendElasticsearch || backend == knowledge.RetrievalBackendMilvus {
		s.retrievalBackend = backend
	}
	return s
}

func (s *Service) ConfigureEmbeddingMetric(metric string) *Service {
	if knowledge.ValidEmbeddingMetric(metric) {
		s.embeddingMetric = knowledge.NormalizeEmbeddingMetric(metric)
	}
	return s
}

func (s *Service) ConfigureRetrievalBackends(retrievers map[string]retrieval.Retriever, indexers map[string]retrieval.Indexer) *Service {
	s.retrievers = make(map[string]retrieval.Retriever, len(retrievers))
	for name, backend := range retrievers {
		if backend != nil {
			s.retrievers[strings.TrimSpace(name)] = backend
		}
	}
	s.indexers = make(map[string]retrieval.Indexer, len(indexers))
	for name, backend := range indexers {
		if backend != nil {
			s.indexers[strings.TrimSpace(name)] = backend
		}
	}
	return s
}

// ConfigurePythonChunking enables explicit python:* selection after the
// shadow benchmark has been reviewed.
func (s *Service) ConfigurePythonChunking(enabled bool, methods ...string) *Service {
	s.pythonChunkingEnabled = enabled
	s.pythonChunkMethods = nil
	if !enabled {
		return s
	}
	if len(methods) == 0 {
		methods = []string{"python:fixed_token", "python:recursive"}
	}
	s.pythonChunkMethods = make(map[string]struct{}, len(methods))
	for _, method := range methods {
		method = strings.TrimSpace(method)
		if validChunkMethod(method) && strings.HasPrefix(method, "python:") {
			s.pythonChunkMethods[method] = struct{}{}
		}
	}
	return s
}

type CreateKnowledgeBaseRequest struct {
	Name                string  `json:"name" binding:"required"`
	Description         string  `json:"description"`
	RetrievalBackend    string  `json:"retrieval_backend"`
	RetrievalMode       string  `json:"retrieval_mode"`
	EmbeddingProviderID *int64  `json:"embedding_provider_id"`
	EmbeddingModel      string  `json:"embedding_model"`
	EmbeddingDimensions int     `json:"embedding_dimensions"`
	EmbeddingMetric     string  `json:"embedding_metric"`
	VectorWeight        float64 `json:"vector_weight"`
	RerankEnabled       bool    `json:"rerank_enabled"`
	RerankProviderID    *int64  `json:"rerank_provider_id"`
	RerankModel         string  `json:"rerank_model"`
	ChunkMethod         string  `json:"chunk_method"`
	ChunkSize           int     `json:"chunk_size"`
	ChunkOverlap        int     `json:"chunk_overlap"`
}

type UpdateKnowledgeBaseRequest struct {
	Name                *string  `json:"name"`
	Description         *string  `json:"description"`
	RetrievalBackend    *string  `json:"retrieval_backend"`
	RetrievalMode       *string  `json:"retrieval_mode"`
	EmbeddingProviderID *int64   `json:"embedding_provider_id"`
	EmbeddingModel      *string  `json:"embedding_model"`
	EmbeddingDimensions *int     `json:"embedding_dimensions"`
	EmbeddingMetric     *string  `json:"embedding_metric"`
	VectorWeight        *float64 `json:"vector_weight"`
	RerankEnabled       *bool    `json:"rerank_enabled"`
	RerankProviderID    *int64   `json:"rerank_provider_id"`
	RerankModel         *string  `json:"rerank_model"`
	ChunkMethod         *string  `json:"chunk_method"`
	ChunkSize           *int     `json:"chunk_size"`
	ChunkOverlap        *int     `json:"chunk_overlap"`
	Enabled             *bool    `json:"enabled"`
}

type UploadDocumentRequest struct {
	Name        string
	FileHeader  *multipart.FileHeader
	ContentType string
}

type UploadDocumentResponse struct {
	Document *knowledge.Document     `json:"document"`
	Job      *knowledge.IngestionJob `json:"job"`
}

type SearchRequest struct {
	Query string         `json:"query" binding:"required"`
	TopK  int            `json:"top_k"`
	Mode  retrieval.Mode `json:"mode"`
}

type ReindexKnowledgeBaseResponse struct {
	JobCount int64 `json:"job_count"`
}

func NewService(
	kbs knowledge.KnowledgeBaseRepository,
	documents knowledge.DocumentRepository,
	chunks knowledge.ChunkRepository,
	jobs knowledge.IngestionJobRepository,
	logs knowledge.RetrievalLogRepository,
	audits audit.Repository,
	storage FileStorage,
	retriever retrieval.Retriever,
	indexer retrieval.Indexer,
) *Service {
	return &Service{
		kbs:             kbs,
		documents:       documents,
		chunks:          chunks,
		jobs:            jobs,
		logs:            logs,
		audits:          audits,
		storage:         storage,
		retriever:       retriever,
		indexer:         indexer,
		embeddingMetric: knowledge.EmbeddingMetricCosine,
	}
}

func (s *Service) CreateKnowledgeBase(ctx context.Context, ownerID int64, req CreateKnowledgeBaseRequest, client ClientInfo) (*knowledge.KnowledgeBase, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	chunkSize := req.ChunkSize
	if chunkSize == 0 {
		chunkSize = 800
	}
	chunkOverlap := req.ChunkOverlap
	if chunkOverlap == 0 {
		chunkOverlap = 100
	}
	if chunkSize <= 0 || chunkOverlap < 0 || chunkOverlap >= chunkSize {
		return nil, agenterrors.ErrInvalidInput
	}
	chunkMethod := strings.TrimSpace(req.ChunkMethod)
	if chunkMethod == "" {
		chunkMethod = knowledge.ChunkMethodRecursive
	}
	if !s.validChunkMethod(chunkMethod) {
		return nil, agenterrors.ErrInvalidInput
	}
	retrievalMode := strings.TrimSpace(req.RetrievalMode)
	if retrievalMode == "" {
		retrievalMode = knowledge.RetrievalModeKeyword
	}
	if !validRetrievalMode(retrievalMode) {
		return nil, agenterrors.ErrInvalidInput
	}
	vectorWeight := req.VectorWeight
	if vectorWeight == 0 {
		vectorWeight = 0.5
	}
	if vectorWeight < 0 || vectorWeight > 1 || req.EmbeddingDimensions < 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if requiresEmbedding(retrievalMode) && req.EmbeddingProviderID == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	embeddingMetric := knowledge.NormalizeEmbeddingMetric(req.EmbeddingMetric)
	if !knowledge.ValidEmbeddingMetric(embeddingMetric) {
		return nil, agenterrors.ErrInvalidInput
	}
	if strings.TrimSpace(req.EmbeddingMetric) == "" {
		embeddingMetric = s.embeddingMetric
	}

	retrievalBackend := strings.TrimSpace(req.RetrievalBackend)
	if retrievalBackend == "" {
		retrievalBackend = s.retrievalBackend
	}
	if retrievalBackend == "" {
		retrievalBackend = knowledge.RetrievalBackendElasticsearch
	}
	if _, ok := s.retrievers[retrievalBackend]; len(s.retrievers) > 0 && !ok {
		return nil, fmt.Errorf("retrieval backend %q is not configured", retrievalBackend)
	}
	if requiresEmbedding(retrievalMode) && embeddingMetric != knowledge.NormalizeEmbeddingMetric(s.embeddingMetric) {
		return nil, fmt.Errorf("embedding metric %q does not match configured backend metric %q", embeddingMetric, knowledge.NormalizeEmbeddingMetric(s.embeddingMetric))
	}
	kb := &knowledge.KnowledgeBase{
		SoftDeleteModel:     domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}},
		Name:                name,
		Description:         strings.TrimSpace(req.Description),
		RetrievalBackend:    retrievalBackend,
		RetrievalMode:       retrievalMode,
		EmbeddingProviderID: req.EmbeddingProviderID,
		EmbeddingModel:      strings.TrimSpace(req.EmbeddingModel),
		EmbeddingDimensions: req.EmbeddingDimensions,
		EmbeddingMetric:     embeddingMetric,
		VectorWeight:        vectorWeight,
		RerankEnabled:       req.RerankEnabled,
		RerankProviderID:    req.RerankProviderID,
		RerankModel:         strings.TrimSpace(req.RerankModel),
		ChunkMethod:         chunkMethod,
		ChunkSize:           chunkSize,
		ChunkOverlap:        chunkOverlap,
		Enabled:             knowledge.KnowledgeBaseEnabled,
	}
	if err := s.kbs.Create(ctx, kb); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "knowledge_base.create", "knowledge_base", strconv.FormatInt(kb.ID, 10), nil, client)
	return kb, nil
}

func (s *Service) ListKnowledgeBases(ctx context.Context, ownerID int64) ([]knowledge.KnowledgeBase, error) {
	return s.kbs.ListByOwner(ctx, ownerID)
}

func (s *Service) GetKnowledgeBase(ctx context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	kb, err := s.kbs.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return kb, nil
}

func (s *Service) UpdateKnowledgeBase(ctx context.Context, ownerID, id int64, req UpdateKnowledgeBaseRequest, client ClientInfo) (*knowledge.KnowledgeBase, error) {
	kb, err := s.GetKnowledgeBase(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	oldBackend := kb.RetrievalBackend
	oldMode := kb.RetrievalMode
	oldProfile := kb.EmbeddingProfile()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.Name = name
	}
	if req.Description != nil {
		kb.Description = strings.TrimSpace(*req.Description)
	}
	if req.RetrievalBackend != nil {
		backend := strings.TrimSpace(*req.RetrievalBackend)
		if backend != knowledge.RetrievalBackendElasticsearch && backend != knowledge.RetrievalBackendMilvus {
			return nil, agenterrors.ErrInvalidInput
		}
		if len(s.retrievers) > 0 {
			if _, ok := s.retrievers[backend]; !ok {
				return nil, fmt.Errorf("retrieval backend %q is not configured", backend)
			}
		}
		kb.RetrievalBackend = backend
	}
	if req.RetrievalMode != nil {
		mode := strings.TrimSpace(*req.RetrievalMode)
		if !validRetrievalMode(mode) {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.RetrievalMode = mode
	}
	if req.EmbeddingProviderID != nil {
		kb.EmbeddingProviderID = req.EmbeddingProviderID
	}
	if req.EmbeddingModel != nil {
		kb.EmbeddingModel = strings.TrimSpace(*req.EmbeddingModel)
	}
	if req.EmbeddingDimensions != nil {
		if *req.EmbeddingDimensions < 0 {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.EmbeddingDimensions = *req.EmbeddingDimensions
	}
	if req.EmbeddingMetric != nil {
		if !knowledge.ValidEmbeddingMetric(*req.EmbeddingMetric) {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.EmbeddingMetric = knowledge.NormalizeEmbeddingMetric(*req.EmbeddingMetric)
	}
	if req.VectorWeight != nil {
		if *req.VectorWeight < 0 || *req.VectorWeight > 1 {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.VectorWeight = *req.VectorWeight
	}
	if req.RerankEnabled != nil {
		kb.RerankEnabled = *req.RerankEnabled
	}
	if req.RerankProviderID != nil {
		kb.RerankProviderID = req.RerankProviderID
	}
	if req.RerankModel != nil {
		kb.RerankModel = strings.TrimSpace(*req.RerankModel)
	}
	if req.ChunkMethod != nil {
		method := strings.TrimSpace(*req.ChunkMethod)
		if !s.validChunkMethod(method) {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.ChunkMethod = method
	}
	if req.ChunkSize != nil {
		if *req.ChunkSize <= 0 {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.ChunkSize = *req.ChunkSize
	}
	if req.ChunkOverlap != nil {
		if *req.ChunkOverlap < 0 {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.ChunkOverlap = *req.ChunkOverlap
	}
	if kb.ChunkOverlap >= kb.ChunkSize {
		return nil, agenterrors.ErrInvalidInput
	}
	if req.Enabled != nil {
		kb.Enabled = *req.Enabled
	}
	// embedding 相关配置(检索模式 / provider / model)变化时,重置已缓存的向量维度,
	// 使下一次重建索引能按新模型重新推断维度,避免维度校验死锁。
	if req.EmbeddingDimensions == nil {
		embeddingChanged := kb.RetrievalMode != oldMode ||
			kb.EmbeddingProfile().ProviderID != oldProfile.ProviderID ||
			kb.EmbeddingProfile().Model != oldProfile.Model ||
			kb.EmbeddingProfile().Metric != oldProfile.Metric
		if embeddingChanged {
			kb.EmbeddingDimensions = 0
		}
	}
	if requiresEmbedding(kb.RetrievalMode) && kb.EmbeddingProviderID == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	if requiresEmbedding(kb.RetrievalMode) && knowledge.NormalizeEmbeddingMetric(kb.EmbeddingMetric) != knowledge.NormalizeEmbeddingMetric(s.embeddingMetric) {
		return nil, fmt.Errorf("embedding metric %q does not match configured backend metric %q", kb.EmbeddingMetric, knowledge.NormalizeEmbeddingMetric(s.embeddingMetric))
	}
	indexCompatibilityChanged := kb.RetrievalBackend != oldBackend ||
		requiresEmbedding(kb.RetrievalMode) != requiresEmbedding(oldMode) ||
		kb.EmbeddingProfile() != oldProfile
	if indexCompatibilityChanged {
		documents, listErr := s.documents.ListByKnowledgeBase(ctx, ownerID, id)
		if listErr != nil {
			return nil, listErr
		}
		if len(documents) > 0 {
			return nil, fmt.Errorf("%w: retrieval backend or embedding profile changes require an explicit knowledge base rebuild", agenterrors.ErrConflict)
		}
	}
	if err := s.kbs.Update(ctx, kb); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "knowledge_base.update", "knowledge_base", strconv.FormatInt(kb.ID, 10), nil, client)
	return kb, nil
}

func (s *Service) ReindexKnowledgeBase(ctx context.Context, ownerID, id int64, client ClientInfo) (*ReindexKnowledgeBaseResponse, error) {
	if _, err := s.GetKnowledgeBase(ctx, ownerID, id); err != nil {
		return nil, err
	}
	docs, err := s.documents.ListByKnowledgeBase(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	var jobCount int64
	for i := range docs {
		doc := docs[i]
		doc.IngestionStatus = knowledge.DocumentStatusPending
		doc.IngestionError = ""
		if err := s.documents.Update(ctx, &doc); err != nil {
			return nil, err
		}
		job := &knowledge.IngestionJob{
			BaseModel:       domain.BaseModel{OwnerID: ownerID},
			KnowledgeBaseID: id,
			DocumentID:      doc.ID,
			JobType:         knowledge.IngestionJobTypeDocument,
			Status:          knowledge.IngestionJobStatusPending,
			MaxAttempts:     3,
			AttemptCount:    0,
		}
		if err := s.createIngestionJob(ctx, job); err != nil {
			return nil, err
		}
		jobCount++
	}
	_ = s.audit(ctx, ownerID, ownerID, "knowledge_base.reindex", "knowledge_base", strconv.FormatInt(id, 10), map[string]any{"job_count": jobCount}, client)
	return &ReindexKnowledgeBaseResponse{JobCount: jobCount}, nil
}

func (s *Service) DeleteKnowledgeBase(ctx context.Context, ownerID, id int64, client ClientInfo) error {
	if _, err := s.GetKnowledgeBase(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.indexer.DeleteByKnowledgeBase(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.chunks.DeleteByKnowledgeBase(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.documents.SoftDeleteByKnowledgeBase(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.kbs.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	_ = s.audit(ctx, ownerID, ownerID, "knowledge_base.delete", "knowledge_base", strconv.FormatInt(id, 10), nil, client)
	return nil
}

func (s *Service) UploadDocument(ctx context.Context, ownerID, kbID int64, req UploadDocumentRequest, client ClientInfo) (*UploadDocumentResponse, error) {
	kb, err := s.GetKnowledgeBase(ctx, ownerID, kbID)
	if err != nil {
		return nil, err
	}
	if req.FileHeader == nil || req.FileHeader.Size <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	originalFilename := filepath.Base(req.FileHeader.Filename)
	fileType := normalizeFileType(filepath.Ext(originalFilename))
	if !isSupportedDocumentType(fileType) {
		return nil, fmt.Errorf("%w: only txt, md, pdf, png, jpg and jpeg are supported", agenterrors.ErrInvalidInput)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = originalFilename
	}
	doc := &knowledge.Document{
		SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}},
		KnowledgeBaseID:  kb.ID,
		Name:             name,
		OriginalFilename: originalFilename,
		FileType:         fileType,
		MIMEType:         req.ContentType,
		FileSizeBytes:    req.FileHeader.Size,
		IngestionStatus:  knowledge.DocumentStatusPending,
		Enabled:          true,
	}
	if err := s.documents.Create(ctx, doc); err != nil {
		return nil, err
	}

	objectKey := objectKey(ownerID, kbID, doc.ID, originalFilename)
	file, err := req.FileHeader.Open()
	if err != nil {
		doc.IngestionStatus = knowledge.DocumentStatusFailed
		doc.IngestionError = err.Error()
		return nil, errors.Join(err, s.documents.Update(ctx, doc))
	}
	defer file.Close()

	hash := sha256.New()
	reader := io.TeeReader(file, hash)
	if err := s.storage.Put(ctx, objectKey, reader, req.FileHeader.Size, req.ContentType); err != nil {
		doc.IngestionStatus = knowledge.DocumentStatusFailed
		doc.IngestionError = err.Error()
		return nil, errors.Join(err, s.documents.Update(ctx, doc))
	}
	doc.StorageObjectKey = objectKey
	doc.ContentHash = hex.EncodeToString(hash.Sum(nil))
	if err := s.documents.Update(ctx, doc); err != nil {
		return nil, err
	}

	job := &knowledge.IngestionJob{
		BaseModel:       domain.BaseModel{OwnerID: ownerID},
		KnowledgeBaseID: kbID,
		DocumentID:      doc.ID,
		JobType:         knowledge.IngestionJobTypeDocument,
		Status:          knowledge.IngestionJobStatusPending,
		MaxAttempts:     3,
		AttemptCount:    0,
	}
	if err := s.createIngestionJob(ctx, job); err != nil {
		doc.IngestionStatus = knowledge.DocumentStatusFailed
		doc.IngestionError = err.Error()
		return nil, errors.Join(err, s.documents.Update(ctx, doc))
	}
	if err := s.kbs.AdjustCounts(ctx, ownerID, kbID, 1, 0); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "document.upload", "document", strconv.FormatInt(doc.ID, 10), map[string]any{"knowledge_base_id": kbID, "job_id": job.ID}, client)
	return &UploadDocumentResponse{Document: doc, Job: job}, nil
}

func (s *Service) createIngestionJob(ctx context.Context, job *knowledge.IngestionJob) error {
	if err := s.jobs.Create(ctx, job); err != nil {
		return err
	}
	if s.jobQueue == nil {
		return nil
	}
	return s.jobQueue.Publish(ctx, queue.Job{
		ID:   strconv.FormatInt(job.ID, 10),
		Type: job.JobType,
		Payload: map[string]any{
			"owner_id":          job.OwnerID,
			"ingestion_job_id":  job.ID,
			"knowledge_base_id": job.KnowledgeBaseID,
			"document_id":       job.DocumentID,
		},
	})
}

func (s *Service) ListDocuments(ctx context.Context, ownerID, kbID int64) ([]knowledge.Document, error) {
	if _, err := s.GetKnowledgeBase(ctx, ownerID, kbID); err != nil {
		return nil, err
	}
	return s.documents.ListByKnowledgeBase(ctx, ownerID, kbID)
}

func (s *Service) GetDocument(ctx context.Context, ownerID, id int64) (*knowledge.Document, error) {
	doc, err := s.documents.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return doc, nil
}

func (s *Service) DeleteDocument(ctx context.Context, ownerID, id int64, client ClientInfo) error {
	doc, err := s.GetDocument(ctx, ownerID, id)
	if err != nil {
		return err
	}
	if err := s.indexer.DeleteByDocument(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.chunks.DeleteByDocument(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.documents.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	if err := s.kbs.AdjustCounts(ctx, ownerID, doc.KnowledgeBaseID, -1, -doc.ChunkCount); err != nil {
		return err
	}
	_ = s.audit(ctx, ownerID, ownerID, "document.delete", "document", strconv.FormatInt(id, 10), map[string]any{"knowledge_base_id": doc.KnowledgeBaseID}, client)
	return nil
}

func (s *Service) SetDocumentEnabled(ctx context.Context, ownerID, id int64, enabled bool, client ClientInfo) (*knowledge.Document, error) {
	doc, err := s.GetDocument(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if doc.Enabled == enabled {
		return doc, nil
	}
	if err := s.documents.SetEnabled(ctx, ownerID, id, enabled); err != nil {
		return nil, err
	}
	// 同步更新当前检索后端中该文档所有 chunk 的 enabled 标记；禁用后不再命中，启用后恢复命中。
	if err := s.indexer.SetDocumentEnabled(ctx, ownerID, id, enabled); err != nil {
		// MySQL 已更新,ES 同步失败时回滚 MySQL 以保持一致。
		return nil, errors.Join(err, s.documents.SetEnabled(ctx, ownerID, id, doc.Enabled))
	}
	doc.Enabled = enabled
	action := "document.disable"
	if enabled {
		action = "document.enable"
	}
	_ = s.audit(ctx, ownerID, ownerID, action, "document", strconv.FormatInt(id, 10), map[string]any{"knowledge_base_id": doc.KnowledgeBaseID}, client)
	return doc, nil
}

func (s *Service) ListChunks(ctx context.Context, ownerID, documentID int64) ([]knowledge.DocumentChunk, error) {
	if _, err := s.GetDocument(ctx, ownerID, documentID); err != nil {
		return nil, err
	}
	return s.chunks.ListByDocument(ctx, ownerID, documentID)
}

func (s *Service) Search(ctx context.Context, ownerID, kbID int64, req SearchRequest) (*retrieval.RetrievalResponse, error) {
	kb, err := s.GetKnowledgeBase(ctx, ownerID, kbID)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	topK := req.TopK
	if topK == 0 {
		topK = 8
	}
	if topK < 0 || topK > 50 {
		return nil, agenterrors.ErrInvalidInput
	}
	mode := req.Mode
	if mode == "" {
		mode = retrieval.Mode(kb.RetrievalMode)
	}
	if mode == "" {
		mode = retrieval.ModeKeyword
	}
	if !validRetrievalMode(string(mode)) {
		return nil, agenterrors.ErrInvalidInput
	}

	rawRetriever := s.retriever
	if backend, ok := s.retrievers[strings.TrimSpace(kb.RetrievalBackend)]; ok {
		rawRetriever = backend
	}
	if rawRetriever == nil {
		return nil, fmt.Errorf("retriever for knowledge base backend %q is not configured", kb.RetrievalBackend)
	}
	activeGenerations := make(map[int64]string)
	if docs, listErr := s.documents.ListByKnowledgeBase(ctx, ownerID, kbID); listErr == nil {
		for _, doc := range docs {
			if strings.TrimSpace(doc.ActiveGenerationID) != "" {
				activeGenerations[doc.ID] = doc.ActiveGenerationID
			}
		}
	}
	retrievalRequest := retrieval.RetrievalRequest{
		OwnerID:             ownerID,
		KnowledgeBaseIDs:    []int64{kbID},
		ActiveGenerationIDs: activeGenerations,
		Query:               query,
		TopK:                topK,
		Mode:                mode,
		EnableHighlight:     true,
	}
	if mode != retrieval.ModeKeyword {
		retrievalRequest.EmbeddingProfile = kb.EmbeddingProfile().Key()
	}
	resp, err := rawRetriever.Search(ctx, retrievalRequest)
	if err != nil {
		return nil, err
	}
	_ = s.logRetrieval(ctx, ownerID, kbID, kb.RetrievalBackend, query, topK, mode, resp)
	return resp, nil
}

func validRetrievalMode(mode string) bool {
	switch mode {
	case knowledge.RetrievalModeKeyword, knowledge.RetrievalModeVector, knowledge.RetrievalModeHybrid:
		return true
	default:
		return false
	}
}

func requiresEmbedding(mode string) bool {
	return mode == knowledge.RetrievalModeVector || mode == knowledge.RetrievalModeHybrid
}

func validChunkMethod(method string) bool {
	switch method {
	case knowledge.ChunkMethodFixedToken, knowledge.ChunkMethodRecursive, "python:fixed_token", "python:recursive", "python:langchain_recursive":
		return true
	default:
		return false
	}
}

func (s *Service) validChunkMethod(method string) bool {
	if strings.HasPrefix(method, "python:") {
		if !s.pythonChunkingEnabled {
			return false
		}
		_, allowed := s.pythonChunkMethods[method]
		return allowed
	}
	return validChunkMethod(method)
}

func (s *Service) GetIngestionJob(ctx context.Context, ownerID, id int64) (*knowledge.IngestionJob, error) {
	job, err := s.jobs.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return job, nil
}

func (s *Service) logRetrieval(ctx context.Context, ownerID, kbID int64, backend, query string, topK int, mode retrieval.Mode, resp *retrieval.RetrievalResponse) error {
	kbIDsJSON, _ := json.Marshal([]int64{kbID})
	resultsJSON, _ := json.Marshal(resp.Results)
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = s.retrievalBackend
		if backend == "" {
			backend = knowledge.RetrievalBackendElasticsearch
		}
	}
	return s.logs.Create(ctx, &knowledge.RetrievalLog{
		ImmutableModel:   domain.ImmutableModel{OwnerID: ownerID},
		KnowledgeBaseIDs: kbIDsJSON,
		QueryText:        query,
		RetrievalBackend: backend,
		RetrievalMode:    string(mode),
		TopK:             topK,
		ResultCount:      len(resp.Results),
		LatencyMS:        resp.LatencyMS,
		ResultsJSON:      resultsJSON,
	})
}

func (s *Service) audit(
	ctx context.Context,
	ownerID, actorID int64,
	action, resourceType, resourceID string,
	detail map[string]any,
	client ClientInfo,
) error {
	if s.audits == nil {
		return nil
	}
	return s.audits.Create(ctx, audit.NewLog(ownerID, actorID, action, resourceType, resourceID, detail, client.IPAddress, client.UserAgent))
}

func normalizeFileType(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if ext == "markdown" {
		return "md"
	}
	return ext
}

func isSupportedDocumentType(fileType string) bool {
	switch fileType {
	case "txt", "md", "pdf", "png", "jpg", "jpeg":
		return true
	default:
		return false
	}
}

func objectKey(ownerID, kbID, documentID int64, filename string) string {
	name := strings.ReplaceAll(filepath.Base(filename), " ", "_")
	return fmt.Sprintf("users/%d/knowledge-bases/%d/documents/%d/raw/%s", ownerID, kbID, documentID, name)
}

func mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agenterrors.ErrNotFound
	}
	return err
}

func int64PtrValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
