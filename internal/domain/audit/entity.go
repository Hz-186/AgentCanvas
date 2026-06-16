package audit

import "time"

type Log struct {
	ID           int64     `json:"id" gorm:"primary_key;column:id"`
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
