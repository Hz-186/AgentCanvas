package audit

import (
	"encoding/json"
	"time"
)

type Log struct {
	ID           int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64     `json:"owner_id" gorm:"column:owner_id"`
	ActorID      int64     `json:"actor_id" gorm:"column:actor_id"`
	Action       string    `json:"action" gorm:"column:action"`
	ResourceType string    `json:"resource_type" gorm:"column:resource_type"`
	ResourceID   string    `json:"resource_id" gorm:"column:resource_id"`
	DetailJSON   string    `json:"detail_json" gorm:"column:detail_json"`
	IPAddress    string    `json:"ip_address" gorm:"column:ip_address"`
	UserAgent    string    `json:"user_agent" gorm:"column:user_agent"`
	CreatedAt    time.Time `json:"created_at" gorm:"column:created_at"`
}

func (Log) TableName() string { return "audit_logs" }

func NewLog(ownerID, actorID int64, action, resourceType, resourceID string, detail map[string]any, ipAddress, userAgent string) *Log {
	detailJSON := "{}"
	if detail != nil {
		if data, err := json.Marshal(detail); err == nil {
			detailJSON = string(data)
		}
	}
	return &Log{
		OwnerID: ownerID, ActorID: actorID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, DetailJSON: detailJSON,
		IPAddress: ipAddress, UserAgent: userAgent,
	}
}
