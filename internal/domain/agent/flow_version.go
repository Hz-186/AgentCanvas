package agent

import (
	"encoding/json"
	"time"
)

type FlowVersion struct {
	ID          int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID     int64           `json:"owner_id" gorm:"column:owner_id"`
	AgentID     int64           `json:"agent_id" gorm:"column:agent_id"`
	VersionNo   int             `json:"version_no" gorm:"column:version_no"`
	DSLJSON     json.RawMessage `json:"dsl_json" gorm:"column:dsl_json"`
	Description string          `json:"description" gorm:"column:description"`
	IsDraft     bool            `json:"is_draft" gorm:"column:is_draft"`
	IsPublished bool            `json:"is_published" gorm:"column:is_published"`
	CreatedAt   time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (FlowVersion) TableName() string { return "agent_flow_versions" }
