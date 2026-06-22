package dialog

import "time"

const (
	StatusDisabled = iota
	StatusActive
)

type Dialog struct {
	ID                int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID           int64      `json:"owner_id" gorm:"column:owner_id"`
	Name              string     `json:"name" gorm:"column:name"`
	Description       string     `json:"description" gorm:"column:description"`
	ProviderID        int64      `json:"provider_id" gorm:"column:provider_id"`
	Model             string     `json:"model" gorm:"column:model"`
	SystemPrompt      string     `json:"system_prompt" gorm:"column:system_prompt"`
	Prologue          string     `json:"prologue" gorm:"column:prologue"`
	KBIDs             []int64    `json:"kb_ids" gorm:"-"`
	KBIDsJSON         string     `json:"-" gorm:"column:kb_ids"`
	TopK              int        `json:"top_k" gorm:"column:top_k"`
	RetrievalMode     string     `json:"retrieval_mode" gorm:"column:retrieval_mode"`
	HistoryRoundLimit int        `json:"history_round_limit" gorm:"column:history_round_limit"`
	Status            int        `json:"status" gorm:"column:status"`
	CreatedAt         time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt         time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt         *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (Dialog) TableName() string { return "dialogs" }
