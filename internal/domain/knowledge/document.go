package knowledge

import (
	"time"

	"agentcanvas/internal/domain"
)

const (
	DocumentStatusPending   = "pending"
	DocumentStatusParsing   = "parsing"
	DocumentStatusChunking  = "chunking"
	DocumentStatusIndexing  = "indexing"
	DocumentStatusCompleted = "completed"
	DocumentStatusFailed    = "failed"
)

type Document struct {
	domain.SoftDeleteModel
	KnowledgeBaseID    int64      `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`
	Name               string     `json:"name" gorm:"column:name"`
	OriginalFilename   string     `json:"original_filename" gorm:"column:original_filename"`
	FileType           string     `json:"file_type" gorm:"column:file_type"`
	MIMEType           string     `json:"mime_type" gorm:"column:mime_type"`
	FileSizeBytes      int64      `json:"file_size_bytes" gorm:"column:file_size_bytes"`
	StorageObjectKey   string     `json:"storage_object_key" gorm:"column:storage_object_key"`
	ContentHash        string     `json:"content_hash" gorm:"column:content_hash"`
	ActiveGenerationID string     `json:"active_generation_id,omitempty" gorm:"column:active_generation_id"`
	IngestionStatus    string     `json:"ingestion_status" gorm:"column:ingestion_status"`
	IngestionError     string     `json:"ingestion_error,omitempty" gorm:"column:ingestion_error"`
	Enabled            bool       `json:"enabled" gorm:"column:enabled"`
	ChunkCount         int        `json:"chunk_count" gorm:"column:chunk_count"`
	TokenCount         int        `json:"token_count" gorm:"column:token_count"`
	IndexedAt          *time.Time `json:"indexed_at" gorm:"column:indexed_at"`
}

func (Document) TableName() string { return "documents" }
