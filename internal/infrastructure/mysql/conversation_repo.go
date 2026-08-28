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
	if err := r.db.WithContext(ctx).Create(item).Error; err != nil {
		return err
	}
	return nil
}

func (r *ConversationRepository) ListByAgent(ctx context.Context, ownerID, agentID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND agent_id = ? AND deleted_at IS NULL", ownerID, agentID).
		Order("last_message_at DESC, id DESC").
		Find(&items).Error
	return items, err
}

func (r *ConversationRepository) UpdateAgentMode(ctx context.Context, ownerID, id int64, mode string) error {
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
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
	return &item, nil
}

func (r *ConversationRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}
