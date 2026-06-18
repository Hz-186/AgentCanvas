package knowledge

import "time"

const (
	DocumentStatusPending   = "pending"
	DocumentStatusParsing   = "parsing"
	DocumentStatusChunking  = "chunking"
	DocumentStatusIndexing  = "indexing"
	DocumentStatusCompleted = "completed"
	DocumentStatusFailed    = "failed"
)

type Document struct {
	ID               int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64      `json:"owner_id" gorm:"column:owner_id"`
	KBID             int64      `json:"kb_id" gorm:"column:kb_id"`
	Name             string     `json:"name" gorm:"column:name"`
	OriginalFilename string     `json:"original_filename" gorm:"column:original_filename"`
	FileType         string     `json:"file_type" gorm:"column:file_type"`
	MimeType         string     `json:"mime_type" gorm:"column:mime_type"`
	FileSize         int64      `json:"file_size" gorm:"column:file_size"`
	ObjectKey        string     `json:"object_key" gorm:"column:object_key"`
	ContentHash      string     `json:"content_hash" gorm:"column:content_hash"`
	ParserStatus     string     `json:"parser_status" gorm:"column:parser_status"`
	ParserError      string     `json:"parser_error,omitempty" gorm:"column:parser_error"`
	ChunkCount       int        `json:"chunk_count" gorm:"column:chunk_count"`
	TokenCount       int        `json:"token_count" gorm:"column:token_count"`
	IndexedAt        *time.Time `json:"indexed_at" gorm:"column:indexed_at"`
	CreatedAt        time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt        *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (Document) TableName() string { return "documents" }
