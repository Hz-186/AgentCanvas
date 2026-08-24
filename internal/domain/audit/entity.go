package audit

import (
	"encoding/json"

	"agentcanvas/internal/domain"
)

type Log struct {
	domain.ImmutableModel
	ActorID      int64           `json:"actor_id" gorm:"column:actor_id"`
	Action       string          `json:"action" gorm:"column:action"`
	ResourceType string          `json:"resource_type" gorm:"column:resource_type"`
	ResourceID   string          `json:"resource_id" gorm:"column:resource_id"`
	DetailJSON   json.RawMessage `json:"detail_json" gorm:"column:detail_json"`
	IPAddress    string          `json:"ip_address" gorm:"column:ip_address"`
	UserAgent    string          `json:"user_agent" gorm:"column:user_agent"`
}

func (Log) TableName() string { return "audit_logs" }

func NewLog(ownerID, actorID int64, action, resourceType, resourceID string, detail map[string]any, ipAddress, userAgent string) *Log {
	detailJSON := json.RawMessage(`{}`)
	if detail != nil {
		if data, err := json.Marshal(detail); err == nil {
			detailJSON = data
		}
	}
	return &Log{
		ImmutableModel: domain.ImmutableModel{OwnerID: ownerID}, ActorID: actorID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, DetailJSON: detailJSON,
		IPAddress: ipAddress, UserAgent: userAgent,
	}
}
