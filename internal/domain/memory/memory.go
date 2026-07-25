package memory

import (
	"encoding/json"
	"time"
)

const (
	TypeProfile  = "profile_memory"
	TypeSummary  = "summary_memory"
	TypeEpisodic = "episodic_memory"
	TypeTask     = "task_memory"
	TypeArchival = "archival_memory"
)

const (
	LevelWorking   = "working"
	LevelShortTerm = "short_term"
	LevelLongTerm  = "long_term"
)

const (
	WriteActionCreate   = "create"
	WriteActionUpdate   = "update"
	WriteActionNoop     = "noop"
	WriteActionConflict = "conflict"
)

type Memory struct {
	ID                 int64           `json:"id" gorm:"primaryKey;column:id"`
	ParentID           *int64          `json:"parent_id" gorm:"column:parent_id"`
	ConflictFlag       bool            `json:"conflict_flag" gorm:"column:conflict_flag"`
	OwnerID            int64           `json:"owner_id" gorm:"column:owner_id"`
	ConversationID     *int64          `json:"conversation_id" gorm:"column:conversation_id"`
	SessionID          *string         `json:"session_id" gorm:"column:session_id"`
	MemoryType         string          `json:"memory_type" gorm:"column:memory_type"`
	MemoryLevel        string          `json:"memory_level" gorm:"column:memory_level;default:long_term"`
	Title              string          `json:"title" gorm:"column:title"`
	Content            string          `json:"content" gorm:"column:content"`
	Importance         float64         `json:"importance" gorm:"column:importance"`
	AccessCount        int             `json:"access_count" gorm:"column:access_count;default:0"`
	ConsolidationCount int             `json:"consolidation_count" gorm:"column:consolidation_count;default:0"`
	Source             string          `json:"source" gorm:"column:source"`
	SourceKey          *string         `json:"source_key,omitempty" gorm:"column:source_key"`
	MetadataJSON       json.RawMessage `json:"metadata_json" gorm:"column:metadata_json"`
	Embedding          []byte          `json:"-" gorm:"column:embedding"`
	LastUsedAt         *time.Time      `json:"last_used_at" gorm:"column:last_used_at"`
	ExpiresAt          *time.Time      `json:"expires_at" gorm:"column:expires_at"`
	CreatedAt          time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt          *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Memory) TableName() string { return "memories" }

type WriteLog struct {
	ID              int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID         int64           `json:"owner_id" gorm:"column:owner_id"`
	MemoryID        int64           `json:"memory_id" gorm:"column:memory_id"`
	RunID           int64           `json:"run_id" gorm:"column:run_id"`
	SourceMessageID int64           `json:"source_message_id" gorm:"column:source_message_id"`
	Action          string          `json:"action" gorm:"column:action"`
	BeforeJSON      json.RawMessage `json:"before_json" gorm:"column:before_json"`
	AfterJSON       json.RawMessage `json:"after_json" gorm:"column:after_json"`
	Reason          string          `json:"reason" gorm:"column:reason"`
	CreatedAt       time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (WriteLog) TableName() string { return "memory_write_logs" }
