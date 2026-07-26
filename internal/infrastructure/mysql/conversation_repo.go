package mysql

import (
	"context"
	"encoding/json"
	"time"

	"agentcanvas/internal/domain/conversation"

	"gorm.io/gorm"
)

type ConversationRepository struct {
	db *gorm.DB
}

func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

func (r *ConversationRepository) Create(ctx context.Context, item *conversation.Conversation) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	normalizeConversation(item)
	if item.Source == "" {
		item.Source = conversation.SourceRAGChat
	}
	if err := r.db.WithContext(ctx).Create(item).Error; err != nil {
		return err
	}
	hydrateConversation(item)
	return nil
}

func (r *ConversationRepository) ListByOwner(ctx context.Context, ownerID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("last_message_at DESC, id DESC").
		Find(&items).Error
	for i := range items {
		hydrateConversation(&items[i])
	}
	return items, err
}

func (r *ConversationRepository) ListByDialog(ctx context.Context, ownerID, dialogID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND dialog_id = ? AND deleted_at IS NULL", ownerID, dialogID).
		Order("last_message_at DESC, id DESC").
		Find(&items).Error
	for i := range items {
		hydrateConversation(&items[i])
	}
	return items, err
}

func (r *ConversationRepository) ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND workflow_id = ? AND source = ? AND deleted_at IS NULL", ownerID, workflowID, conversation.SourceWorkflow).
		Order("last_message_at DESC, id DESC").
		Find(&items).Error
	for i := range items {
		hydrateConversation(&items[i])
	}
	return items, err
}

func (r *ConversationRepository) ListByAgent(ctx context.Context, ownerID, agentID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND agent_id = ? AND source = ? AND deleted_at IS NULL", ownerID, agentID, conversation.SourceAgent).
		Order("last_message_at DESC, id DESC").
		Find(&items).Error
	for i := range items {
		hydrateConversation(&items[i])
	}
	return items, err
}

func (r *ConversationRepository) UpdateAgentMode(ctx context.Context, ownerID, id int64, mode string) error {
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND source = ? AND deleted_at IS NULL", id, ownerID, conversation.SourceAgent).
		Updates(map[string]any{"agent_mode": mode, "updated_at": time.Now().UTC()}).Error
}

func (r *ConversationRepository) FindByID(ctx context.Context, ownerID, id int64) (*conversation.Conversation, error) {
	var item conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	hydrateConversation(&item)
	return &item, nil
}

func (r *ConversationRepository) Update(ctx context.Context, item *conversation.Conversation) error {
	now := time.Now().UTC()
	item.UpdatedAt = now
	item.LastMessageAt = &now
	normalizeConversation(item)
	if err := r.db.WithContext(ctx).Save(item).Error; err != nil {
		return err
	}
	hydrateConversation(item)
	return nil
}

func (r *ConversationRepository) UpdateLastMessageAt(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"last_message_at": now, "updated_at": now}).Error
}

func normalizeConversation(item *conversation.Conversation) {
	if item.Name != "" {
		item.Title = item.Name
	}
	if item.Title != "" {
		item.Name = item.Title
	}
	if item.Messages != nil {
		raw, _ := json.Marshal(item.Messages)
		item.MessageJSON = string(raw)
	} else if item.MessageJSON == "" {
		item.MessageJSON = "[]"
	}
	if item.References != nil {
		raw, _ := json.Marshal(item.References)
		item.ReferenceJSON = string(raw)
	} else if item.ReferenceJSON == "" {
		item.ReferenceJSON = "[]"
	}
}

func hydrateConversation(item *conversation.Conversation) {
	if item == nil {
		return
	}
	item.Name = item.Title
	if item.MessageJSON != "" {
		_ = json.Unmarshal([]byte(item.MessageJSON), &item.Messages)
	}
	if item.Messages == nil {
		item.Messages = []conversation.MessageItem{}
	}
	if item.ReferenceJSON != "" {
		_ = json.Unmarshal([]byte(item.ReferenceJSON), &item.References)
	}
	if item.References == nil {
		item.References = []conversation.ReferenceItem{}
	}
}

func (r *ConversationRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
