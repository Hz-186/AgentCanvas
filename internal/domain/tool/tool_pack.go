package tool

import "agentcanvas/internal/domain"

type ToolPack struct {
	domain.BaseModel
	Name        string `json:"name" gorm:"column:name"`
	Description string `json:"description,omitempty" gorm:"column:description"`
}

func (ToolPack) TableName() string { return "tool_packs" }

type ToolPackItem struct {
	domain.ImmutableModel
	ToolPackID int64 `json:"tool_pack_id" gorm:"column:tool_pack_id"`
	ToolID     int64 `json:"tool_id" gorm:"column:tool_id"`
}

func (ToolPackItem) TableName() string { return "tool_pack_items" }
