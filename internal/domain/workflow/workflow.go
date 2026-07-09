package workflow

import (
	"time"

	"agentcanvas/internal/domain"
)

const (
	StatusDisabled = domain.StatusDisabled
	StatusActive   = domain.StatusActive
)

type Workflow struct {
	ID               int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64      `json:"owner_id" gorm:"column:owner_id"`
	Name             string     `json:"name" gorm:"column:name"`
	Description      string     `json:"description" gorm:"column:description"`
	AvatarURL        string     `json:"avatar_url" gorm:"column:avatar_url"`
	CurrentVersionID *int64     `json:"current_version_id" gorm:"column:current_version_id"`
	Status           int        `json:"status" gorm:"column:status"`
	CreatedAt        time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt        *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (Workflow) TableName() string { return "workflows" }
