package provider_usecase

import (
	"agentcanvas/internal/domain/audit"
	providerdomain "agentcanvas/internal/domain/provider"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Service struct {
	providers providerdomain.Repository
	audits    audit.Repository
	secrets   *cryptoinfra.SecretBox
	tester    llm.ProviderTester
}

type ClientInfo struct {
	UserAgent string
	IPAddress string
}

type CreateProviderRequest struct {
	Name                  string `json:"name" binding:"required"`
	ProviderType          string `json:"provider_type" binding:"required"`
	BaseURL               string `json:"base_url"`
	APIKey                string `json:"api_key"`
	DefaultChatModel      string `json:"default_chat_model"`
	DefaultEmbeddingModel string `json:"default_embedding_model"`
}

type UpdateProviderRequest struct {
	Name                  *string `json:"name"`
	ProviderType          *string `json:"provider_type"`
	BaseURL               *string `json:"base_url"`
	APIKey                *string `json:"api_key"`
	DefaultChatModel      *string `json:"default_chat_model"`
	DefaultEmbeddingModel *string `json:"default_embedding_model"`
	Status                *int    `json:"status"`
}

func NewService(providers providerdomain.Repository, audits audit.Repository, secrets *cryptoinfra.SecretBox, tester llm.ProviderTester) *Service {
	return &Service{
		providers: providers,
		audits:    audits,
		secrets:   secrets,
		tester:    tester,
	}
}

func (s *Service) Create(ctx context.Context, ownerID int64, req CreateProviderRequest, client ClientInfo) (*providerdomain.ModelProvider, error) {
	if strings.TrimSpace(req.Name) == "" || !validProviderType(req.ProviderType) {
		return nil, agenterrors.ErrInvalidInput
	}
	encrypted, mask, err := s.encryptKey(req.APIKey)
	if err != nil {
		return nil, err
	}
	p := &providerdomain.ModelProvider{
		OwnerID:               ownerID,
		Name:                  strings.TrimSpace(req.Name),
		ProviderType:          req.ProviderType,
		BaseURL:               strings.TrimSpace(req.BaseURL),
		EncryptedAPIKey:       encrypted,
		APIKeyMask:            mask,
		DefaultChatModel:      req.DefaultChatModel,
		DefaultEmbeddingModel: req.DefaultEmbeddingModel,
		Status:                providerdomain.StatusActive,
	}
	if err := s.providers.Create(ctx, p); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "model_provider.create", "model_provider", strconv.FormatInt(p.ID, 10), map[string]any{"provider_type": p.ProviderType}, client)
	return sanitizeProvider(p), nil
}

func (s *Service) List(ctx context.Context, ownerID int64) ([]providerdomain.ModelProvider, error) {
	providers, err := s.providers.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	for i := range providers {
		providers[i].EncryptedAPIKey = ""
	}
	return providers, nil
}

func (s *Service) Get(ctx context.Context, ownerID, id int64) (*providerdomain.ModelProvider, error) {
	p, err := s.providers.FindByID(ctx, ownerID, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	return sanitizeProvider(p), nil
}

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateProviderRequest, client ClientInfo) (*providerdomain.ModelProvider, error) {
	p, err := s.providers.FindByID(ctx, ownerID, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	if req.Name != nil {
		p.Name = strings.TrimSpace(*req.Name)
	}
	if req.ProviderType != nil {
		if !validProviderType(*req.ProviderType) {
			return nil, agenterrors.ErrInvalidInput
		}
		p.ProviderType = *req.ProviderType
	}
	if req.BaseURL != nil {
		p.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if req.APIKey != nil {
		encrypted, mask, err := s.encryptKey(*req.APIKey)
		if err != nil {
			return nil, err
		}
		p.EncryptedAPIKey = encrypted
		p.APIKeyMask = mask
	}
	if req.DefaultChatModel != nil {
		p.DefaultChatModel = *req.DefaultChatModel
	}
	if req.DefaultEmbeddingModel != nil {
		p.DefaultEmbeddingModel = *req.DefaultEmbeddingModel
	}
	if req.Status != nil {
		p.Status = *req.Status
	}
	if err := s.providers.Update(ctx, p); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "model_provider.update", "model_provider", strconv.FormatInt(id, 10), nil, client)
	return sanitizeProvider(p), nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64, client ClientInfo) error {
	if err := s.providers.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	_ = s.audit(ctx, ownerID, ownerID, "model_provider.delete", "model_provider", strconv.FormatInt(id, 10), nil, client)
	return nil
}

func (s *Service) Test(ctx context.Context, ownerID, id int64, client ClientInfo) (*providerdomain.ModelProvider, error) {
	p, err := s.providers.FindByID(ctx, ownerID, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	apiKey, err := s.secrets.Decrypt(p.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	p.LastTestAt = &now
	if err := s.tester.Test(ctx, llm.ProviderTestConfig{
		ProviderType: p.ProviderType,
		BaseURL:      p.BaseURL,
		APIKey:       apiKey,
	}); err != nil {
		p.LastTestStatus = "failed"
		p.LastTestError = err.Error()
		_ = s.providers.Update(ctx, p)
		return sanitizeProvider(p), nil
	}
	p.LastTestStatus = "success"
	p.LastTestError = ""
	if err := s.providers.Update(ctx, p); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, ownerID, ownerID, "model_provider.test", "model_provider", strconv.FormatInt(id, 10), map[string]any{"status": p.LastTestStatus}, client)
	return sanitizeProvider(p), nil
}

func (s *Service) encryptKey(apiKey string) (encrypted string, mask string, err error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", nil
	}
	encrypted, err = s.secrets.Encrypt(apiKey)
	if err != nil {
		return "", "", err
	}
	return encrypted, cryptoinfra.MaskSecret(apiKey), nil
}

func validProviderType(providerType string) bool {
	switch providerType {
	case providerdomain.TypeOpenAICompatible, providerdomain.TypeDeepSeek, providerdomain.TypeQwen, providerdomain.TypeOllama, providerdomain.TypeAzureOpenAI, providerdomain.TypeLocal:
		return true
	default:
		return false
	}
}

func sanitizeProvider(p *providerdomain.ModelProvider) *providerdomain.ModelProvider {
	clone := *p
	clone.EncryptedAPIKey = ""
	return &clone
}

func (s *Service) audit(ctx context.Context, ownerID, actorID int64, action, resourceType, resourceID string, detail map[string]any, client ClientInfo) error {
	detailJSON := "{}"
	if detail != nil {
		if data, err := json.Marshal(detail); err == nil {
			detailJSON = string(data)
		}
	}
	return s.audits.Create(ctx, &audit.Log{
		OwnerID:      ownerID,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		DetailJSON:   detailJSON,
		IPAddress:    client.IPAddress,
		UserAgent:    client.UserAgent,
	})
}
