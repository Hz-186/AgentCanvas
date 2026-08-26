package conversation

import (
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain"
)

const (
	ModeDefault = "default"
	ModePlan    = "plan"
)

// NormalizeMode keeps legacy aliases at the persistence/API boundary while
// making every newly produced collaboration-mode value canonical.
func NormalizeMode(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", ModeDefault, "goal", "react":
		return ModeDefault, nil
	case ModePlan, "plan_execute":
		return ModePlan, nil
	default:
		return "", fmt.Errorf("mode must be default or plan")
	}
}

type Conversation struct {
	domain.SoftDeleteModel
	Title                string     `json:"title" gorm:"column:title"`
	AgentID              *int64     `json:"agent_id,omitempty" gorm:"column:agent_id"`
	ProjectID            *int64     `json:"project_id,omitempty" gorm:"column:project_id"`
	WorkspaceMode        string     `json:"workspace_mode" gorm:"column:workspace_mode"`
	AgentMode            string     `json:"agent_mode,omitempty" gorm:"column:agent_mode"`
	ParentConversationID *int64     `json:"parent_conversation_id,omitempty" gorm:"column:parent_conversation_id"`
	LastMessageAt        *time.Time `json:"last_message_at" gorm:"column:last_message_at"`
}

func (Conversation) TableName() string { return "conversations" }
