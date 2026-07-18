package conversation

import (
	"context"
	"time"
)

const (
	CompactionTriggerAuto   = "auto"
	CompactionTriggerManual = "manual"
	CompactionCompleted     = "completed"
	CompactionFallback      = "fallback"
	CompactionFailed        = "failed"
)

type Compaction struct {
	ID                int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID           int64     `json:"owner_id" gorm:"column:owner_id"`
	ConversationID    int64     `json:"conversation_id" gorm:"column:conversation_id"`
	FirstMessageID    int64     `json:"first_message_id" gorm:"column:first_message_id"`
	LastMessageID     int64     `json:"last_message_id" gorm:"column:last_message_id"`
	SourceFingerprint string    `json:"source_fingerprint" gorm:"column:source_fingerprint"`
	TriggerType       string    `json:"trigger_type" gorm:"column:trigger_type"`
	Status            string    `json:"status" gorm:"column:status"`
	Summary           string    `json:"summary" gorm:"column:summary"`
	PromptVersion     string    `json:"prompt_version" gorm:"column:prompt_version"`
	ProviderID        int64     `json:"provider_id" gorm:"column:provider_id"`
	Model             string    `json:"model" gorm:"column:model"`
	BeforeTokens      int       `json:"before_tokens" gorm:"column:before_tokens"`
	AfterTokens       int       `json:"after_tokens" gorm:"column:after_tokens"`
	ErrorMessage      string    `json:"error_message,omitempty" gorm:"column:error_message"`
	CreatedAt         time.Time `json:"created_at" gorm:"column:created_at"`
}

func (Compaction) TableName() string { return "conversation_compactions" }

type CompactionRepository interface {
	Create(context.Context, *Compaction) error
	FindByFingerprint(context.Context, int64, int64, string) (*Compaction, error)
	FindLatest(context.Context, int64, int64) (*Compaction, error)
}
