package conversation

import (
	"context"
	"time"

	"agentcanvas/internal/domain"
)

const (
	CompactionTriggerAuto    = "auto"
	CompactionTriggerManual  = "manual"
	CompactionTriggerRuntime = "runtime"
	CompactionSummaryPrefix  = "SUMMARY:\n"
	CompactionCompleted      = "completed"
)

type Compaction struct {
	domain.ImmutableModel
	ConversationID      int64      `json:"conversation_id" gorm:"column:conversation_id"`
	FirstMessageID      int64      `json:"first_message_id" gorm:"column:first_message_id"`
	LastMessageID       int64      `json:"last_message_id" gorm:"column:last_message_id"`
	FirstMessageContent string     `json:"first_message_content,omitempty" gorm:"column:first_message_content"`
	ParentSnapshotID    *int64     `json:"parent_snapshot_id,omitempty" gorm:"column:parent_snapshot_id"`
	SnapshotVersion     int        `json:"snapshot_version" gorm:"column:snapshot_version"`
	SourceFingerprint   string     `json:"source_fingerprint" gorm:"column:source_fingerprint"`
	TriggerType         string     `json:"trigger_type" gorm:"column:trigger_type"`
	Status              string     `json:"status" gorm:"column:status"`
	Summary             string     `json:"summary" gorm:"column:summary"`
	PromptVersion       string     `json:"prompt_version" gorm:"column:prompt_version"`
	PromptHash          string     `json:"prompt_hash" gorm:"column:prompt_hash"`
	ProviderID          int64      `json:"provider_id" gorm:"column:provider_id"`
	Model               string     `json:"model" gorm:"column:model"`
	BeforeTokens        int        `json:"before_tokens" gorm:"column:before_tokens"`
	AfterTokens         int        `json:"after_tokens" gorm:"column:after_tokens"`
	SummaryTokens       int        `json:"summary_tokens" gorm:"column:summary_tokens"`
	WindowNumber        int        `json:"window_number" gorm:"column:window_number"`
	ContextWindowTokens int        `json:"context_window_tokens" gorm:"column:context_window_tokens"`
	ErrorMessage        string     `json:"error_message,omitempty" gorm:"column:error_message"`
	CompletedAt         *time.Time `json:"completed_at,omitempty" gorm:"column:completed_at"`
}

func (Compaction) TableName() string { return "conversation_compactions" }

type SnapshotRepository interface {
	FindCurrentSnapshot(context.Context, int64, int64) (*Compaction, error)
	ClaimSnapshot(context.Context, int64, int64, *int64, int, string, time.Time) (bool, error)
	CompleteSnapshot(context.Context, *Compaction, *int64, int, string) error
	ReleaseSnapshotClaim(context.Context, int64, int64, string, string) error
}
