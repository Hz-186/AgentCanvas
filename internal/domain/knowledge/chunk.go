package knowledge

import "time"

type DocumentChunk struct {
	ID           int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64     `json:"owner_id" gorm:"column:owner_id"`
	KBID         int64     `json:"kb_id" gorm:"column:kb_id"`
	DocumentID   int64     `json:"document_id" gorm:"column:document_id"`
	ChunkIndex   int       `json:"chunk_index" gorm:"column:chunk_index"`
	Content      string    `json:"content" gorm:"column:content"`
	ContentHash  string    `json:"content_hash" gorm:"column:content_hash"`
	TokenCount   int       `json:"token_count" gorm:"column:token_count"`
	CharCount    int       `json:"char_count" gorm:"column:char_count"`
	PageNo       *int      `json:"page_no" gorm:"column:page_no"`
	SectionTitle string    `json:"section_title" gorm:"column:section_title"`
	ESIndex      string    `json:"es_index" gorm:"column:es_index"`
	ESDocID      string    `json:"es_doc_id" gorm:"column:es_doc_id"`
	MetadataJSON string    `json:"metadata_json" gorm:"column:metadata_json"`
	CreatedAt    time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (DocumentChunk) TableName() string { return "document_chunks" }
