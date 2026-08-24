package auth

import (
	"time"

	"agentcanvas/internal/domain"
)

type OAuthAccount struct {
	ID             int64     `json:"id" gorm:"primaryKey;column:id"`
	UserID         int64     `json:"user_id" gorm:"column:user_id"`
	Provider       string    `json:"provider" gorm:"column:provider"`
	ProviderUserID string    `json:"provider_user_id" gorm:"column:provider_user_id"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
}

func (OAuthAccount) TableName() string { return "oauth_accounts" }

type Session struct {
	ID               int64      `json:"id" gorm:"primaryKey;column:id"`
	UserID           int64      `json:"user_id" gorm:"column:user_id"`
	RefreshTokenHash string     `json:"-" gorm:"column:refresh_token_hash"`
	ExpiresAt        time.Time  `json:"expires_at" gorm:"column:expires_at"`
	RevokedAt        *time.Time `json:"revoked_at" gorm:"column:revoked_at"`
	CreatedAt        time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (Session) TableName() string { return "auth_sessions" }

type APIToken struct {
	domain.BaseModel
	Name        string     `json:"name" gorm:"column:name"`
	TokenHash   string     `json:"-" gorm:"column:token_hash"`
	TokenPrefix string     `json:"token_prefix" gorm:"column:token_prefix"`
	Scopes      string     `json:"scopes" gorm:"column:scopes;type:json"`
	ExpiresAt   *time.Time `json:"expires_at" gorm:"column:expires_at"`
	RevokedAt   *time.Time `json:"revoked_at" gorm:"column:revoked_at"`
}

func (APIToken) TableName() string { return "api_tokens" }
