package conversation

import "time"

const (
	SourceRAGChat  = "rag_chat"
	SourceWorkflow = "workflow"
)

type Conversation struct {
	ID            int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64           `json:"owner_id" gorm:"column:owner_id"`
	DialogID      *int64          `json:"dialog_id,omitempty" gorm:"column:dialog_id"`
	Title         string          `json:"title" gorm:"column:title"`
	Name          string          `json:"name" gorm:"-"`
	Source        string          `json:"source" gorm:"column:source"`
	WorkflowID    *int64          `json:"workflow_id,omitempty" gorm:"column:workflow_id"`
	Messages      []MessageItem   `json:"messages" gorm:"-"`
	MessageJSON   string          `json:"-" gorm:"column:message_json"`
	References    []ReferenceItem `json:"references" gorm:"-"`
	ReferenceJSON string          `json:"-" gorm:"column:reference_json"`
	LastMessageAt *time.Time      `json:"last_message_at" gorm:"column:last_message_at"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt     *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Conversation) TableName() string { return "conversations" }

type MessageItem struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type ReferenceItem struct {
	MessageID string           `json:"message_id"`
	Chunks    []ReferenceChunk `json:"chunks"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
}

type ReferenceChunk struct {
	KBID       int64          `json:"kb_id"`
	DocumentID int64          `json:"document_id"`
	ChunkID    int64          `json:"chunk_id"`
	RefIndex   int            `json:"ref_index"`
	Score      float64        `json:"score"`
	QuoteText  string         `json:"quote_text"`
	PageNo     *int           `json:"page_no,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
