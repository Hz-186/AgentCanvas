package mysql

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	mysqldriver "github.com/go-sql-driver/mysql"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		created := true
		if transcriptIdentityUsable(message) {
			existing, err := findTranscriptMessage(tx, message)
			switch {
			case err == nil:
				if err := verifyTranscriptPayload(existing, message); err != nil {
					return err
				}
				*message = *existing
				created = false
			case errors.Is(err, gorm.ErrRecordNotFound):
				if err := tx.Create(message).Error; err != nil {
					if !isDuplicateKey(err) {
						return err
					}
					// A concurrent writer may have won the unique-key race. The
					// locking read is a current read, so it sees that committed row
					// even under MySQL's default repeatable-read isolation.
					existing, lookupErr := findTranscriptMessage(tx, message)
					if lookupErr != nil {
						return lookupErr
					}
					if verifyErr := verifyTranscriptPayload(existing, message); verifyErr != nil {
						return verifyErr
					}
					*message = *existing
					created = false
				}
			default:
				return err
			}
		} else if err := tx.Create(message).Error; err != nil {
			return err
		}
		if !created {
			return nil
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

func transcriptIdentityUsable(message *conversation.Message) bool {
	return message != nil && message.RunID != nil && message.TranscriptEntryID != nil && strings.TrimSpace(*message.TranscriptEntryID) != ""
}

func findTranscriptMessage(tx *gorm.DB, message *conversation.Message) (*conversation.Message, error) {
	var existing conversation.Message
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("owner_id = ? AND conversation_id = ? AND run_id = ? AND transcript_entry_id = ?", message.OwnerID, message.ConversationID, *message.RunID, *message.TranscriptEntryID).
		First(&existing).Error
	return &existing, err
}

func verifyTranscriptPayload(existing, incoming *conversation.Message) error {
	if existing == nil || incoming == nil {
		return conversation.ErrTranscriptEntryConflict
	}
	existingContentType := existing.ContentType
	if existingContentType == "" {
		existingContentType = conversation.ContentTypeText
	}
	incomingContentType := incoming.ContentType
	if incomingContentType == "" {
		incomingContentType = conversation.ContentTypeText
	}
	if existing.OwnerID != incoming.OwnerID || existing.ConversationID != incoming.ConversationID ||
		existing.Role != incoming.Role || existing.Content != incoming.Content ||
		existingContentType != incomingContentType || existing.TokenCount != incoming.TokenCount ||
		!sameOptionalInt64(existing.RunID, incoming.RunID) || !sameOptionalString(existing.TranscriptEntryID, incoming.TranscriptEntryID) ||
		!bytes.Equal(existing.MetadataJSON, incoming.MetadataJSON) {
		return fmt.Errorf("%w: %s", conversation.ErrTranscriptEntryConflict, strings.TrimSpace(*incoming.TranscriptEntryID))
	}
	return nil
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate")
}
