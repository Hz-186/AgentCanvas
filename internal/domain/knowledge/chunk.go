package knowledge

import (
	"encoding/json"

	"agentcanvas/internal/domain"
)

type DocumentChunk struct {
	domain.ImmutableModel
	KnowledgeBaseID int64           `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`
	DocumentID      int64           `json:"document_id" gorm:"column:document_id"`
	GenerationID    string          `json:"generation_id,omitempty" gorm:"column:generation_id"`
	ChunkIndex      int             `json:"chunk_index" gorm:"column:chunk_index"`
	Content         string          `json:"content" gorm:"column:content"`
	ContentHash     string          `json:"content_hash" gorm:"column:content_hash"`
	TokenCount      int             `json:"token_count" gorm:"column:token_count"`
	CharCount       int             `json:"char_count" gorm:"column:char_count"`
	PageNumber      *int            `json:"page_number" gorm:"column:page_number"`
	SectionTitle    string          `json:"section_title" gorm:"column:section_title"`
	MetadataJSON    json.RawMessage `json:"metadata_json" gorm:"column:metadata_json"`
}

func (DocumentChunk) TableName() string { return "document_chunks" }
