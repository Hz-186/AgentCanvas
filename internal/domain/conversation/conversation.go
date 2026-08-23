package conversation

import (
	"time"

	"agentcanvas/internal/domain"
)

const ()

type Conversation struct {
	domain.SoftDeleteModel
	Title                string     `json:"title" gorm:"column:title"`
	AgentID              *int64     `json:"agent_id,omitempty" gorm:"column:agent_id"`
	AgentReleaseID       *int64     `json:"agent_release_id,omitempty" gorm:"column:agent_release_id"`
	ProjectID            *int64     `json:"project_id,omitempty" gorm:"column:project_id"`
	WorkspaceMode        string     `json:"workspace_mode" gorm:"column:workspace_mode"`
	AgentMode            string     `json:"agent_mode,omitempty" gorm:"column:agent_mode"`
	ParentConversationID *int64     `json:"parent_conversation_id,omitempty" gorm:"column:parent_conversation_id"`
	LastMessageAt        *time.Time `json:"last_message_at" gorm:"column:last_message_at"`
}

func (Conversation) TableName() string { return "conversations" }
