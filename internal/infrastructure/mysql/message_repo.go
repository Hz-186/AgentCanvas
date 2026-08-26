package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/contextresource"
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
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(message).Error; err != nil {
			return err
		}
		agentID := int64(0)
		if err := tx.Raw("SELECT COALESCE(agent_id, 0) FROM conversations WHERE owner_id = ? AND id = ?", message.OwnerID, message.ConversationID).Scan(&agentID).Error; err != nil {
			return err
		}
		if message.ContentType == conversation.ContentTypeText {
			if err := enqueueContextResource(ctx, tx, message.OwnerID, agentID, message.ConversationID, contextresource.TypeConversationMessage, message.ID, contextresource.OperationUpsert, messageContextText(*message)); err != nil {
				return err
			}
		}
		return touchConversationLastMessage(ctx, tx, message.OwnerID, message.ConversationID, message.CreatedAt)
	})
}

func (r *MessageRepository) ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).
		Order("id ASC").
		Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL", ownerID, conversationID).
		Order("id ASC").
		Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) ListActiveAfter(ctx context.Context, ownerID, conversationID, afterMessageID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id > ?", ownerID, conversationID, afterMessageID).
		Order("id ASC").Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) ListActiveBetween(ctx context.Context, ownerID, conversationID, firstMessageID, lastMessageID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id >= ? AND id <= ?", ownerID, conversationID, firstMessageID, lastMessageID).
		Order("id ASC").Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) ListActiveThrough(ctx context.Context, ownerID, conversationID, throughMessageID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id <= ?", ownerID, conversationID, throughMessageID).
		Order("id ASC").Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) LatestActiveMessageID(ctx context.Context, ownerID, conversationID int64) (int64, error) {
	var id int64
	err := r.db.WithContext(ctx).Model(&conversation.Message{}).
		Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL", ownerID, conversationID).
		Order("id DESC").Limit(1).Pluck("id", &id).Error
	return id, err
}

func (r *MessageRepository) ListActiveAfterThrough(ctx context.Context, ownerID, conversationID, afterMessageID, throughMessageID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id > ? AND id <= ?", ownerID, conversationID, afterMessageID, throughMessageID).
		Order("id ASC").Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]conversation.Message, error) {
	var messages []conversation.Message
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Order("id ASC").
		Find(&messages).Error
	return messages, err
}

func (r *MessageRepository) ArchiveConversationMessages(ctx context.Context, ownerID, conversationID int64, archivedAt time.Time) (int64, error) {
	return r.ArchiveConversationMessagesThrough(ctx, ownerID, conversationID, 1<<62, archivedAt)
}

func (r *MessageRepository) ArchiveConversationMessagesThrough(ctx context.Context, ownerID, conversationID, throughMessageID int64, archivedAt time.Time) (int64, error) {
	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	}
	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var messages []conversation.Message
		if err := tx.Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id <= ?", ownerID, conversationID, throughMessageID).Find(&messages).Error; err != nil {
			return err
		}
		result := tx.Model(&conversation.Message{}).Where("owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id <= ?", ownerID, conversationID, throughMessageID).
			Updates(map[string]any{"archived_at": archivedAt})
		if result.Error != nil {
			return result.Error
		}
		affected = result.RowsAffected
		for i := range messages {
			if err := enqueueContextResource(ctx, tx, ownerID, 0, messages[i].ConversationID, contextresource.TypeConversationMessage, messages[i].ID, contextresource.OperationDelete, ""); err != nil {
				return err
			}
		}
		return nil
	})
	return affected, err
}

func touchConversationLastMessage(ctx context.Context, tx *gorm.DB, ownerID, conversationID int64, lastMessageAt time.Time) error {
	if conversationID <= 0 {
		return nil
	}
	return tx.WithContext(ctx).Model(&conversation.Conversation{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", conversationID, ownerID).
		Updates(map[string]any{"last_message_at": lastMessageAt, "updated_at": time.Now().UTC()}).Error
}
