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
	jobQueue              queue.JobQueue
	pythonChunkingEnabled bool
}

type ClientInfo struct {
	UserAgent string
	IPAddress string
}

func (s *Service) WithJobQueue(jobQueue queue.JobQueue) *Service {
	s.jobQueue = jobQueue
	return s
}

// ConfigurePythonChunking enables explicit python:* selection after the
// shadow benchmark has been reviewed.
func (s *Service) ConfigurePythonChunking(enabled bool) *Service {
	s.pythonChunkingEnabled = enabled
	return s
}

type CreateKnowledgeBaseRequest struct {
	Name                string  `json:"name" binding:"required"`
	Description         string  `json:"description"`
	RetrievalMode       string  `json:"retrieval_mode"`
	EmbeddingProviderID *int64  `json:"embedding_provider_id"`
	EmbeddingModel      string  `json:"embedding_model"`
	EmbeddingDimensions int     `json:"embedding_dimensions"`
	HybridWeight        float64 `json:"hybrid_weight"`
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
	RetrievalMode       *string  `json:"retrieval_mode"`
	EmbeddingProviderID *int64   `json:"embedding_provider_id"`
	EmbeddingModel      *string  `json:"embedding_model"`
	EmbeddingDimensions *int     `json:"embedding_dimensions"`
	HybridWeight        *float64 `json:"hybrid_weight"`
	RerankEnabled       *bool    `json:"rerank_enabled"`
	RerankProviderID    *int64   `json:"rerank_provider_id"`
	RerankModel         *string  `json:"rerank_model"`
	ChunkMethod         *string  `json:"chunk_method"`
	ChunkSize           *int     `json:"chunk_size"`
	ChunkOverlap        *int     `json:"chunk_overlap"`
	Status              *int     `json:"status"`
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
		kbs:       kbs,
		documents: documents,
		chunks:    chunks,
		jobs:      jobs,
		logs:      logs,
		audits:    audits,
		storage:   storage,
		retriever: retriever,
		indexer:   indexer,
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
	hybridWeight := req.HybridWeight
	if hybridWeight == 0 {
		hybridWeight = 0.5
	}
	if hybridWeight < 0 || hybridWeight > 1 || req.EmbeddingDimensions < 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if requiresEmbedding(retrievalMode) && req.EmbeddingProviderID == nil {
		return nil, agenterrors.ErrInvalidInput
	}

	kb := &knowledge.KnowledgeBase{
		OwnerID:             ownerID,
		Name:                name,
		Description:         strings.TrimSpace(req.Description),
		RetrievalBackend:    knowledge.RetrievalBackendElasticsearch,
		RetrievalMode:       retrievalMode,
		EmbeddingProviderID: req.EmbeddingProviderID,
		EmbeddingModel:      strings.TrimSpace(req.EmbeddingModel),
		EmbeddingDimensions: req.EmbeddingDimensions,
		HybridWeight:        hybridWeight,
		RerankEnabled:       req.RerankEnabled,
		RerankProviderID:    req.RerankProviderID,
		RerankModel:         strings.TrimSpace(req.RerankModel),
		ChunkMethod:         chunkMethod,
		ChunkSize:           chunkSize,
		ChunkOverlap:        chunkOverlap,
		Status:              knowledge.KnowledgeBaseStatusActive,
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
	oldMode := kb.RetrievalMode
	oldModel := kb.EmbeddingModel
	oldProvider := int64PtrValue(kb.EmbeddingProviderID)
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
	if req.HybridWeight != nil {
		if *req.HybridWeight < 0 || *req.HybridWeight > 1 {
			return nil, agenterrors.ErrInvalidInput
		}
		kb.HybridWeight = *req.HybridWeight
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
	if req.Status != nil {
		kb.Status = *req.Status
	}
	// embedding 相关配置(检索模式 / provider / model)变化时,重置已缓存的向量维度,
	// 使下一次重建索引能按新模型重新推断维度,避免维度校验死锁。
	if req.EmbeddingDimensions == nil {
		embeddingChanged := kb.RetrievalMode != oldMode ||
			kb.EmbeddingModel != oldModel ||
			int64PtrValue(kb.EmbeddingProviderID) != oldProvider
		if embeddingChanged {
			kb.EmbeddingDimensions = 0
		}
	}
	if requiresEmbedding(kb.RetrievalMode) && kb.EmbeddingProviderID == nil {
		return nil, agenterrors.ErrInvalidInput
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
		doc.ParserStatus = knowledge.DocumentStatusPending
		doc.ParserError = ""
		if err := s.documents.Update(ctx, &doc); err != nil {
			return nil, err
		}
		job := &knowledge.IngestionJob{
			OwnerID:      ownerID,
			KBID:         id,
			DocumentID:   doc.ID,
			JobType:      knowledge.IngestionJobTypeDocument,
			Status:       knowledge.IngestionJobStatusPending,
			MaxAttempts:  3,
			AttemptCount: 0,
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
		OwnerID:          ownerID,
		KBID:             kb.ID,
		Name:             name,
		OriginalFilename: originalFilename,
		FileType:         fileType,
		MimeType:         req.ContentType,
		FileSize:         req.FileHeader.Size,
		ParserStatus:     knowledge.DocumentStatusPending,
		Enabled:          true,
	}
	if err := s.documents.Create(ctx, doc); err != nil {
		return nil, err
	}

	objectKey := objectKey(ownerID, kbID, doc.ID, originalFilename)
	file, err := req.FileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hash := sha256.New()
	reader := io.TeeReader(file, hash)
	if err := s.storage.Put(ctx, objectKey, reader, req.FileHeader.Size, req.ContentType); err != nil {
		doc.ParserStatus = knowledge.DocumentStatusFailed
		doc.ParserError = err.Error()
		_ = s.documents.Update(ctx, doc)
		return nil, err
	}
	doc.ObjectKey = objectKey
	doc.ContentHash = hex.EncodeToString(hash.Sum(nil))
	if err := s.documents.Update(ctx, doc); err != nil {
		return nil, err
	}

	job := &knowledge.IngestionJob{
		OwnerID:      ownerID,
		KBID:         kbID,
		DocumentID:   doc.ID,
		JobType:      knowledge.IngestionJobTypeDocument,
		Status:       knowledge.IngestionJobStatusPending,
		MaxAttempts:  3,
		AttemptCount: 0,
	}
	if err := s.createIngestionJob(ctx, job); err != nil {
		doc.ParserStatus = knowledge.DocumentStatusFailed
		doc.ParserError = err.Error()
		_ = s.documents.Update(ctx, doc)
		return nil, err
	}
	if err := s.kbs.AdjustCounts(ctx, ownerID, kbID, 1, 0); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "document.upload", "document", strconv.FormatInt(doc.ID, 10), map[string]any{"kb_id": kbID, "job_id": job.ID}, client)
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
			"owner_id":         job.OwnerID,
			"ingestion_job_id": job.ID,
			"kb_id":            job.KBID,
			"document_id":      job.DocumentID,
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
	if err := s.kbs.AdjustCounts(ctx, ownerID, doc.KBID, -1, -doc.ChunkCount); err != nil {
		return err
	}
	_ = s.audit(ctx, ownerID, ownerID, "document.delete", "document", strconv.FormatInt(id, 10), map[string]any{"kb_id": doc.KBID}, client)
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
	// 同步更新 ES 中该文档所有 chunk 的 enabled 标记;禁用后检索不再命中,启用后恢复命中。
	if err := s.indexer.SetDocumentEnabled(ctx, ownerID, id, enabled); err != nil {
		// MySQL 已更新,ES 同步失败时回滚 MySQL 以保持一致。
		_ = s.documents.SetEnabled(ctx, ownerID, id, doc.Enabled)
		return nil, err
	}
	doc.Enabled = enabled
	action := "document.disable"
	if enabled {
		action = "document.enable"
	}
	_ = s.audit(ctx, ownerID, ownerID, action, "document", strconv.FormatInt(id, 10), map[string]any{"kb_id": doc.KBID}, client)
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

	resp, err := s.retriever.Search(ctx, retrieval.RetrievalRequest{
		OwnerID:         ownerID,
		KBIDs:           []int64{kbID},
		Query:           query,
		TopK:            topK,
		Mode:            mode,
		EnableHighlight: true,
	})
	if err != nil {
		return nil, err
	}
	_ = s.logRetrieval(ctx, ownerID, kbID, query, topK, mode, resp)
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
	case knowledge.ChunkMethodFixedToken, knowledge.ChunkMethodRecursive, "python:fixed_token", "python:recursive":
		return true
	default:
		return false
	}
}

func (s *Service) validChunkMethod(method string) bool {
	if strings.HasPrefix(method, "python:") && !s.pythonChunkingEnabled {
		return false
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

func (s *Service) logRetrieval(ctx context.Context, ownerID, kbID int64, query string, topK int, mode retrieval.Mode, resp *retrieval.RetrievalResponse) error {
	kbIDsJSON, _ := json.Marshal([]int64{kbID})
	resultsJSON, _ := json.Marshal(resp.Results)
	return s.logs.Create(ctx, &knowledge.RetrievalLog{
		OwnerID:          ownerID,
		KBIDsJSON:        string(kbIDsJSON),
		QueryText:        query,
		RetrievalBackend: knowledge.RetrievalBackendElasticsearch,
		RetrievalMode:    string(mode),
		TopK:             topK,
		ResultCount:      len(resp.Results),
		LatencyMS:        resp.LatencyMS,
		ResultsJSON:      string(resultsJSON),
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
	detailJSON := "{}"
	if detail != nil {
		if data, err := json.Marshal(detail); err == nil {
			detailJSON = string(data)
		}
	}
	return s.audits.Create(ctx, &audit.Log{
		OwnerID:      ownerID,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		DetailJSON:   detailJSON,
		IPAddress:    client.IPAddress,
		UserAgent:    client.UserAgent,
	})
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
