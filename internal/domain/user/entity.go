package user

import (
	"time"

	"agentcanvas/internal/domain"
)

const (
	LoginTypePassword = "password"
	LoginTypeGithub   = "github"
)

const (
	StatusActive = domain.StatusActive
)

type User struct {
	ID           int64      `json:"id" gorm:"primaryKey;column:id"`
	Username     string     `json:"username" gorm:"column:username"`
	Email        string     `json:"email" gorm:"column:email"`
	PasswordHash string     `json:"-" gorm:"column:password_hash"`
	AvatarURL    string     `json:"avatar_url" gorm:"column:avatar_url"`
	LoginType    string     `json:"login_type" gorm:"column:login_type"`
	Status       int        `json:"status" gorm:"column:status"`
	LastLoginAt  *time.Time `json:"last_login_at" gorm:"column:last_login_at"`
	CreatedAt    time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt    *time.Time `json:"deleted_at" gorm:"column:deleted_at"`
}

func (User) TableName() string { return "users" }
