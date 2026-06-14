package auth

import "time"

type OAuthAccount struct {
	ID                    int64      `json:"id" gorm:"primaryKey;column:id"`
	UserID                int64      `json:"user_id" gorm:"column:user_id"`
	Provider              string     `json:"provider" gorm:"column:provider"` // "github"
	ProviderUserID        string     `json:"provider_user_id" gorm:"column:provider_user_id"`
	ProviderUsername      string     `json:"provider_username" gorm:"column:provider_username"`
	ProviderEmail         string     `json:"provider_email" gorm:"column:provider_email"`
	AvatarURL             string     `json:"avatar_url" gorm:"column:avatar_url"`
	AccessTokenEncrypted  string     `json:"-" gorm:"column:access_token_encrypted"`
	RefreshTokenEncrypted string     `json:"-" gorm:"column:refresh_token_encrypted"`
	Scopes                string     `json:"scopes" gorm:"column:scopes"`
	TokenExpiresAt        *time.Time `json:"token_expires_at" gorm:"column:token_expires_at"`
	CreatedAt             time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt             time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (OAuthAccount) TableName() string { return "oauth_accounts" }

type Session struct {
	ID               int64      `json:"id" gorm:"primaryKey;column:id"`
	UserID           int64      `json:"user_id" gorm:"column:user_id"`
	RefreshTokenHash string     `json:"-" gorm:"column:refresh_token_hash"`
	UserAgent        string     `json:"user_agent" gorm:"column:user_agent"`
	IPAddress        string     `json:"ip_address" gorm:"column:ip_address"`
	ExpiresAt        time.Time  `json:"expires_at" gorm:"column:expires_at"`
	RevokedAt        *time.Time `json:"revoked_at" gorm:"column:revoked_at"`
	CreatedAt        time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (Session) TableName() string { return "auth_sessions" }

type APIToken struct {
	ID          int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID     int64      `json:"owner_id" gorm:"column:owner_id"`
	Name        string     `json:"name" gorm:"column:name"`
	TokenHash   string     `json:"-" gorm:"column:token_hash"`
	TokenPrefix string     `json:"token_prefix" gorm:"column:token_prefix"`
	Scopes      string     `json:"scopes" gorm:"column:scopes;type:json"`
	LastUsedAt  *time.Time `json:"last_used_at" gorm:"column:last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at" gorm:"column:expires_at"`
	RevokedAt   *time.Time `json:"revoked_at" gorm:"column:revoked_at"`
	CreatedAt   time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (APIToken) TableName() string { return "api_tokens" }
