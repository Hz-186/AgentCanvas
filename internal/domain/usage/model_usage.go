package usage

import "time"

const (
	TypeChat = "chat"
)

type ModelUsageLog struct {
	ID               int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64     `json:"owner_id" gorm:"column:owner_id"`
	ProviderID       int64     `json:"provider_id" gorm:"column:provider_id"`
	ProviderType     string    `json:"provider_type" gorm:"column:provider_type"`
	ModelName        string    `json:"model_name" gorm:"column:model_name"`
	UsageType        string    `json:"usage_type" gorm:"column:usage_type"`
	PromptTokens     int       `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens" gorm:"column:completion_tokens"`
	TotalTokens      int       `json:"total_tokens" gorm:"column:total_tokens"`
	EstimatedCost    float64   `json:"estimated_cost" gorm:"column:estimated_cost"`
	LatencyMS        int       `json:"latency_ms" gorm:"column:latency_ms"`
	Success          bool      `json:"success" gorm:"column:success"`
	ErrorMessage     string    `json:"error_message,omitempty" gorm:"column:error_message"`
	RequestID        string    `json:"request_id,omitempty" gorm:"column:request_id"`
	CreatedAt        time.Time `json:"created_at" gorm:"column:created_at"`
}

func (ModelUsageLog) TableName() string { return "model_usage_logs" }
