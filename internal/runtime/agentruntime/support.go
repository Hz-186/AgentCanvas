package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	providerdomain "agentcanvas/internal/domain/provider"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeevent "agentcanvas/internal/runtime/event"
)

func emitRuntimeEvent(ctx context.Context, rc *RunContext, event runtimeevent.Event) {
	if rc == nil || rc.Events == nil {
		return
	}
	_ = rc.Events.Emit(ctx, event)
}

func validateSimpleJSONSchema(schema json.RawMessage, value any) error {
	if len(schema) == 0 || string(schema) == "null" {
		return nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(schema, &cfg); err != nil {
		return fmt.Errorf("invalid json schema")
	}
	if typ, _ := cfg["type"].(string); typ != "" {
		if err := validateJSONType(typ, value); err != nil {
			return err
		}
	}
	properties, _ := cfg["properties"].(map[string]any)
	required, _ := cfg["required"].([]any)
	if len(properties) == 0 && len(required) == 0 {
		return nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("json schema expects object")
	}
	for _, item := range required {
		key, _ := item.(string)
		if key != "" {
			if _, exists := obj[key]; !exists {
				return fmt.Errorf("json schema missing required field %s", key)
			}
		}
	}
	for key, spec := range properties {
		fieldValue, exists := obj[key]
		if !exists {
			continue
		}
		specMap, ok := spec.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := specMap["type"].(string); typ != "" {
			if err := validateJSONType(typ, fieldValue); err != nil {
				return fmt.Errorf("json schema field %s: %w", key, err)
			}
		}
	}
	return nil
}

func validateJSONType(typ string, value any) error {
	switch typ {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("expected object")
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("expected array")
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case "number":
		if _, ok := value.(float64); !ok {
			if _, ok := value.(int); !ok {
				return fmt.Errorf("expected number")
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	}
	return nil
}

type ProviderLoader struct {
	Providers providerdomain.Repository
	Secrets   *cryptoinfra.SecretBox
}

func (l ProviderLoader) LoadChatProviderConfig(ctx context.Context, ownerID, providerID int64, model string) (*LoadedProvider, error) {
	provider, err := l.Providers.FindByID(ctx, ownerID, providerID)
	if err != nil {
		return nil, err
	}
	if provider.Status != providerdomain.StatusActive || l.Secrets == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	apiKey, err := l.Secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	selectedModel := strings.TrimSpace(model)
	if selectedModel == "" {
		selectedModel = strings.TrimSpace(provider.DefaultChatModel)
	}
	if selectedModel == "" {
		return nil, fmt.Errorf("%w: model is required", agenterrors.ErrInvalidInput)
	}
	return &LoadedProvider{
		ProviderID: provider.ID,
		Model:      selectedModel,
		Config: llm.ChatProviderConfig{
			ProviderType: provider.ProviderType,
			BaseURL:      provider.BaseURL,
			APIKey:       apiKey,
		},
		EmbeddingConfig: llm.EmbeddingProviderConfig{
			ProviderType: provider.ProviderType,
			BaseURL:      provider.BaseURL,
			APIKey:       apiKey,
		},
		EmbeddingModel: strings.TrimSpace(provider.DefaultEmbeddingModel),
	}, nil
}

type ConversationMessageWriter struct {
	Messages conversation.MessageRepository
}

func (w ConversationMessageWriter) WriteAssistantMessage(ctx context.Context, ownerID int64, conversationID *int64, runID int64, content string, tokenCount int) (int64, error) {
	if conversationID == nil || *conversationID <= 0 || w.Messages == nil {
		return 0, nil
	}
	message := &conversation.Message{
		OwnerID:        ownerID,
		ConversationID: *conversationID,
		Role:           conversation.RoleAssistant,
		Content:        content,
		ContentType:    conversation.ContentTypeText,
		RunID:          &runID,
		TokenCount:     tokenCount,
	}
	if err := w.Messages.Create(ctx, message); err != nil {
		return 0, err
	}
	return message.ID, nil
}
