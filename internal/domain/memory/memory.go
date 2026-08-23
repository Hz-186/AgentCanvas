package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain"
)

const (
	TypeProfile  = "profile"
	TypeEpisodic = "episodic"
	TypeTask     = "task"
	TypeArchival = "archival"
)

const (
	TierShortTerm = "short_term"
	TierLongTerm  = "long_term"
)

const (
	ScopeUser         = "user"
	ScopeAgent        = "agent"
	ScopeConversation = "conversation"
	ScopeProject      = "project"

	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusRevoked    = "revoked"
)

const (
	WriteActionCreate   = "create"
	WriteActionUpdate   = "update"
	WriteActionNoop     = "noop"
	WriteActionConflict = "conflict"
)

type Memory struct {
	domain.SoftDeleteModel
	ConflictWithID       *int64          `json:"conflict_with_id,omitempty" gorm:"column:conflict_with_id"`
	HasConflict          bool            `json:"has_conflict" gorm:"column:has_conflict"`
	SourceConversationID *int64          `json:"source_conversation_id,omitempty" gorm:"column:source_conversation_id"`
	SourceProjectID      *int64          `json:"source_project_id,omitempty" gorm:"column:source_project_id"`
	ScopeType            string          `json:"scope_type" gorm:"column:scope_type;default:user"`
	ScopeID              int64           `json:"scope_id" gorm:"column:scope_id"`
	Status               string          `json:"status" gorm:"column:status;default:active"`
	SupersedesID         *int64          `json:"supersedes_id,omitempty" gorm:"column:supersedes_id"`
	MemoryType           string          `json:"memory_type" gorm:"column:memory_type"`
	RetentionTier        string          `json:"retention_tier" gorm:"column:retention_tier;default:long_term"`
	Title                string          `json:"title" gorm:"column:title"`
	Content              string          `json:"content" gorm:"column:content"`
	Importance           float64         `json:"importance" gorm:"column:importance"`
	RecallCount          int             `json:"recall_count" gorm:"column:recall_count;default:0"`
	PromotionCount       int             `json:"promotion_count" gorm:"column:promotion_count;default:0"`
	Source               string          `json:"source" gorm:"column:source"`
	DeduplicationKey     *string         `json:"deduplication_key,omitempty" gorm:"column:deduplication_key"`
	MetadataJSON         json.RawMessage `json:"metadata_json" gorm:"column:metadata_json"`
	LastRecalledAt       *time.Time      `json:"last_recalled_at" gorm:"column:last_recalled_at"`
	LastDecayAt          *time.Time      `json:"last_decay_at" gorm:"column:last_decay_at"`
	ExpiresAt            *time.Time      `json:"expires_at" gorm:"column:expires_at"`
}

func (Memory) TableName() string { return "memories" }

// ApplyV2Defaults fills only lifecycle and explicit scope defaults. Source
// identifiers are provenance fields and must never determine access scope.
func (m *Memory) ApplyV2Defaults() {
	if m == nil {
		return
	}
	if m.Status == "" {
		m.Status = StatusActive
	}
	if m.ScopeType == "" {
		m.ScopeType = ScopeUser
	}
	if m.ScopeID <= 0 {
		switch m.ScopeType {
		case ScopeUser:
			m.ScopeID = m.OwnerID
		}
	}
}

func ResolveScope(memoryType string, ownerID, agentID, projectID, conversationID int64, requestedType string, requestedID int64) (string, int64, error) {
	memoryType = strings.TrimSpace(memoryType)
	switch memoryType {
	case TypeProfile, TypeEpisodic, TypeTask, TypeArchival:
	default:
		return "", 0, fmt.Errorf("unsupported memory type %q", memoryType)
	}
	scopeType, scopeID := strings.TrimSpace(requestedType), requestedID
	if scopeType == "" {
		switch memoryType {
		case TypeProfile:
			scopeType, scopeID = ScopeUser, ownerID
		case TypeTask, TypeArchival:
			if projectID > 0 {
				scopeType, scopeID = ScopeProject, projectID
			} else if conversationID > 0 {
				scopeType, scopeID = ScopeConversation, conversationID
			} else {
				return "", 0, fmt.Errorf("memory type %q requires a project or conversation scope", memoryType)
			}
		case TypeEpisodic:
			if conversationID <= 0 {
				return "", 0, fmt.Errorf("memory type %q requires a conversation scope", memoryType)
			}
			scopeType, scopeID = ScopeConversation, conversationID
		default:
			return "", 0, fmt.Errorf("unsupported memory type %q", memoryType)
		}
	}
	if scopeType != ScopeUser && scopeType != ScopeAgent && scopeType != ScopeProject && scopeType != ScopeConversation {
		return "", 0, fmt.Errorf("unsupported memory scope %q", scopeType)
	}
	if scopeID <= 0 {
		switch scopeType {
		case ScopeUser:
			scopeID = ownerID
		case ScopeAgent:
			scopeID = agentID
		case ScopeProject:
			scopeID = projectID
		case ScopeConversation:
			scopeID = conversationID
		}
	}
	if scopeID <= 0 {
		return "", 0, fmt.Errorf("memory scope %q requires a positive scope id", scopeType)
	}
	return scopeType, scopeID, nil
}

func (m Memory) IsRecallable(now time.Time) bool {
	status := m.Status
	if status == "" {
		status = StatusActive
	}
	return status == StatusActive && m.DeletedAt == nil && !m.HasConflict &&
		(m.ExpiresAt == nil || m.ExpiresAt.After(now))
}

type WriteLog struct {
	domain.ImmutableModel
	MemoryID   int64           `json:"memory_id" gorm:"column:memory_id"`
	RunID      int64           `json:"run_id" gorm:"column:run_id"`
	Action     string          `json:"action" gorm:"column:action"`
	BeforeJSON json.RawMessage `json:"before_json" gorm:"column:before_json"`
	AfterJSON  json.RawMessage `json:"after_json" gorm:"column:after_json"`
	Reason     string          `json:"reason" gorm:"column:reason"`
}

func (WriteLog) TableName() string { return "memory_write_logs" }

type RecallLog struct {
	domain.ImmutableModel
	AgentID        int64           `json:"agent_id" gorm:"column:agent_id"`
	ConversationID int64           `json:"conversation_id" gorm:"column:conversation_id"`
	RunID          int64           `json:"run_id" gorm:"column:run_id"`
	Query          string          `json:"query" gorm:"column:query"`
	CandidateJSON  json.RawMessage `json:"candidate_json" gorm:"column:candidate_json"`
	InjectedJSON   json.RawMessage `json:"injected_json" gorm:"column:injected_json"`
	TokenCost      int             `json:"token_cost" gorm:"column:token_cost"`
	Feedback       string          `json:"feedback" gorm:"column:feedback"`
}

func (RecallLog) TableName() string { return "memory_recall_logs" }
