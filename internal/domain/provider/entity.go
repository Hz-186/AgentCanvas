package provider

import (
	"time"

	"agentcanvas/internal/domain"
)

const (
	TypeOpenAICompatible = "openai_compatible"
	TypeDeepSeek         = "deepseek"
	TypeQwen             = "qwen"
	TypeOllama           = "ollama"
	TypeAzureOpenAI      = "azure_openai"
	TypeLocal            = "local"
)

const (
	ProviderEnabled = true
)

type ModelProvider struct {
	domain.SoftDeleteModel
	Name                  string     `json:"name" gorm:"column:name"`
	ProviderType          string     `json:"provider_type" gorm:"column:provider_type"`
	BaseURL               string     `json:"base_url" gorm:"column:base_url"`
	EncryptedAPIKey       string     `json:"-" gorm:"column:encrypted_api_key"`
	APIKeyMask            string     `json:"api_key_mask" gorm:"column:api_key_mask"`
	DefaultChatModel      string     `json:"default_chat_model" gorm:"column:default_chat_model"`
	DefaultEmbeddingModel string     `json:"default_embedding_model" gorm:"column:default_embedding_model"`
	Enabled               bool       `json:"enabled" gorm:"column:enabled"`
	LastTestStatus        string     `json:"last_test_status" gorm:"column:last_test_status"`
	LastTestError         string     `json:"last_test_error,omitempty" gorm:"column:last_test_error"`
	LastTestAt            *time.Time `json:"last_test_at" gorm:"column:last_test_at"`
}

func (ModelProvider) TableName() string { return "model_providers" }

func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "****"
	}
	return secret[:4] + "****" + secret[len(secret)-4:]
}
