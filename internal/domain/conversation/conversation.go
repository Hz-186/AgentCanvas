package conversation

import "time"

const (
	SourceRAGChat = "rag_chat"
)

type Conversation struct {
	ID            int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64      `json:"owner_id" gorm:"column:owner_id"`
	Title         string     `json:"title" gorm:"column:title"`
	Source        string     `json:"source" gorm:"column:source"`
	AgentID       *int64     `json:"agent_id,omitempty" gorm:"column:agent_id"`
	LastMessageAt *time.Time `json:"last_message_at" gorm:"column:last_message_at"`
	CreatedAt     time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt     *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (Conversation) TableName() string { return "conversations" }
