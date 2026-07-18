package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/conversation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConversationCompactionRepository struct{ db *gorm.DB }

func NewConversationCompactionRepository(db *gorm.DB) *ConversationCompactionRepository {
	return &ConversationCompactionRepository{db: db}
}

func (r *ConversationCompactionRepository) Create(ctx context.Context, item *conversation.Compaction) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "conversation_id"}, {Name: "source_fingerprint"}}, DoNothing: true}).Create(item).Error
}

func (r *ConversationCompactionRepository) FindByFingerprint(ctx context.Context, ownerID, conversationID int64, fingerprint string) (*conversation.Compaction, error) {
	var item conversation.Compaction
	err := r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ? AND source_fingerprint = ?", ownerID, conversationID, fingerprint).First(&item).Error
	return &item, err
}

func (r *ConversationCompactionRepository) FindLatest(ctx context.Context, ownerID, conversationID int64) (*conversation.Compaction, error) {
	var item conversation.Compaction
	err := r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).Order("id DESC").First(&item).Error
	return &item, err
}

var _ conversation.CompactionRepository = (*ConversationCompactionRepository)(nil)
