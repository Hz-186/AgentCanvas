package workflow

import (
	"encoding/json"
	"time"
)

type RunEvent struct {
	ID          int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID     int64           `json:"owner_id" gorm:"column:owner_id"`
	RunID       int64           `json:"run_id" gorm:"column:run_id"`
	EventType   string          `json:"event_type" gorm:"column:event_type"`
	NodeID      string          `json:"node_id" gorm:"column:node_id"`
	NodeType    string          `json:"node_type" gorm:"column:node_type"`
	PayloadJSON json.RawMessage `json:"payload_json" gorm:"column:payload_json"`
	CreatedAt   time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (RunEvent) TableName() string { return "workflow_run_events" }
