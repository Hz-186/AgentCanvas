package domain

import "time"

type BaseModel struct {
	ID        int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID   int64     `json:"owner_id" gorm:"column:owner_id"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at"`
}

type SoftDeleteModel struct {
	BaseModel
	DeletedAt *time.Time `json:"-" gorm:"column:deleted_at"`
}

type ImmutableModel struct {
	ID        int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID   int64     `json:"owner_id" gorm:"column:owner_id"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
}
