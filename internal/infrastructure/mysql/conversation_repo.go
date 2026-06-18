package mysql

import (
	"context"
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
	if item.Source == "" {
		item.Source = conversation.SourceRAGChat
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ConversationRepository) ListByOwner(ctx context.Context, ownerID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("last_message_at DESC, id DESC").
		Find(&items).Error
	return items, err
}

func (r *ConversationRepository) FindByID(ctx context.Context, ownerID, id int64) (*conversation.Conversation, error) {
	var item conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ConversationRepository) UpdateLastMessageAt(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"last_message_at": now, "updated_at": now}).Error
}

func (r *ConversationRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
