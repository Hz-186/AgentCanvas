package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/conversation"

	"gorm.io/gorm"
)

type MessageRepository struct {
	db *gorm.DB
}

func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

func (r *MessageRepository) Create(ctx context.Context, message *conversation.Message) error {
	message.CreatedAt = time.Now().UTC()
	if message.ContentType == "" {
		message.ContentType = conversation.ContentTypeText
	}
	return r.db.WithContext(ctx).Create(message).Error
}

func (r *MessageRepository) ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).
		Order("id ASC").
		Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) CreateReferences(ctx context.Context, refs []conversation.MessageReference) error {
	if len(refs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for i := range refs {
		refs[i].CreatedAt = now
	}
	return r.db.WithContext(ctx).Create(&refs).Error
}

func (r *MessageRepository) ListReferencesByMessage(ctx context.Context, ownerID, messageID int64) ([]conversation.MessageReference, error) {
	var refs []conversation.MessageReference
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND message_id = ?", ownerID, messageID).
		Order("ref_index ASC").
		Find(&refs).Error
	return refs, err
}
