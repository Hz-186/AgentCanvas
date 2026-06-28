package tool

import "time"

type ToolPack struct {
	ID          int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID     int64     `json:"owner_id" gorm:"column:owner_id"`
	Name        string    `json:"name" gorm:"column:name"`
	Description string    `json:"description,omitempty" gorm:"column:description"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (ToolPack) TableName() string { return "tool_packs" }

type ToolPackItem struct {
	ID        int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID   int64     `json:"owner_id" gorm:"column:owner_id"`
	PackID    int64     `json:"pack_id" gorm:"column:pack_id"`
	ToolID    int64     `json:"tool_id" gorm:"column:tool_id"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
}

func (ToolPackItem) TableName() string { return "tool_pack_items" }
