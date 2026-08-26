package conversation

import (
	"time"

	"agentcanvas/internal/domain"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
	RoleDeveloper = "developer"
)

type Message struct {
	domain.ImmutableModel
	ConversationID int64      `json:"conversation_id" gorm:"column:conversation_id"`
	Role           string     `json:"role" gorm:"column:role"`
	Content        string     `json:"content" gorm:"column:content"`
	RunID          *int64     `json:"run_id,omitempty" gorm:"column:run_id"`
	TokenCount     int        `json:"token_count" gorm:"column:token_count"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty" gorm:"column:archived_at"`
}

func (Message) TableName() string { return "messages" }
