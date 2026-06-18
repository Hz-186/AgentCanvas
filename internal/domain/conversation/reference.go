package conversation

import "time"

type MessageReference struct {
	ID           int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64     `json:"owner_id" gorm:"column:owner_id"`
	MessageID    int64     `json:"message_id" gorm:"column:message_id"`
	KBID         int64     `json:"kb_id" gorm:"column:kb_id"`
	DocumentID   int64     `json:"document_id" gorm:"column:document_id"`
	ChunkID      int64     `json:"chunk_id" gorm:"column:chunk_id"`
	RefIndex     int       `json:"ref_index" gorm:"column:ref_index"`
	Score        float64   `json:"score" gorm:"column:score"`
	QuoteText    string    `json:"quote_text" gorm:"column:quote_text"`
	PageNo       *int      `json:"page_no,omitempty" gorm:"column:page_no"`
	MetadataJSON string    `json:"metadata_json,omitempty" gorm:"column:metadata_json"`
	CreatedAt    time.Time `json:"created_at" gorm:"column:created_at"`
}

func (MessageReference) TableName() string { return "message_references" }
