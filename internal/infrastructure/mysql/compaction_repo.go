package mysql

import (
	"context"
	"fmt"
	"time"

	"agentcanvas/internal/domain/conversation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConversationCompactionRepository struct{ db *gorm.DB }

type conversationSnapshotClaim struct {
	OwnerID          int64     `gorm:"column:owner_id;primaryKey"`
	ConversationID   int64     `gorm:"column:conversation_id;primaryKey"`
	ParentSnapshotID *int64    `gorm:"column:parent_snapshot_id"`
	ParentVersion    int       `gorm:"column:parent_version"`
	ClaimToken       string    `gorm:"column:claim_token"`
	ExpiresAt        time.Time `gorm:"column:expires_at"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (conversationSnapshotClaim) TableName() string { return "conversation_snapshot_claims" }

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

func (r *ConversationCompactionRepository) FindCurrentSnapshot(ctx context.Context, ownerID, conversationID int64) (*conversation.Compaction, error) {
	var item conversation.Compaction
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND snapshot_version > 0 AND status = ?", ownerID, conversationID, conversation.CompactionCompleted).
		Order("snapshot_version DESC, id DESC").First(&item).Error
	return &item, err
}

func (r *ConversationCompactionRepository) ClaimSnapshot(ctx context.Context, ownerID, conversationID int64, parentID *int64, parentVersion int, token string, expiresAt time.Time) (bool, error) {
	claimed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? AND conversation_id = ? AND expires_at <= ?", ownerID, conversationID, time.Now().UTC()).Delete(&conversationSnapshotClaim{}).Error; err != nil {
			return err
		}
		var current conversation.Compaction
		currentErr := tx.Where("owner_id = ? AND conversation_id = ? AND snapshot_version > 0 AND status = ?", ownerID, conversationID, conversation.CompactionCompleted).
			Order("snapshot_version DESC, id DESC").First(&current).Error
		if currentErr != nil && currentErr != gorm.ErrRecordNotFound {
			return currentErr
		}
		if currentErr == gorm.ErrRecordNotFound {
			if parentID != nil || parentVersion != 0 {
				return nil
			}
		} else if parentID == nil || current.ID != *parentID || current.SnapshotVersion != parentVersion {
			return nil
		}
		now := time.Now().UTC()
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&conversationSnapshotClaim{
			OwnerID: ownerID, ConversationID: conversationID, ParentSnapshotID: parentID, ParentVersion: parentVersion,
			ClaimToken: token, ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
		})
		if result.Error != nil {
			return result.Error
		}
		claimed = result.RowsAffected == 1
		return nil
	})
	return claimed, err
}

func (r *ConversationCompactionRepository) CompleteSnapshot(ctx context.Context, item *conversation.Compaction, parentID *int64, parentVersion int, token string) error {
	if item == nil {
		return fmt.Errorf("conversation snapshot is required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var claim conversationSnapshotClaim
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND conversation_id = ? AND claim_token = ?", item.OwnerID, item.ConversationID, token).First(&claim).Error; err != nil {
			return err
		}
		if claim.ParentVersion != parentVersion || !sameNullableID(claim.ParentSnapshotID, parentID) || !claim.ExpiresAt.After(time.Now().UTC()) {
			return fmt.Errorf("conversation snapshot claim no longer matches parent")
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return tx.Where("owner_id = ? AND conversation_id = ? AND claim_token = ?", item.OwnerID, item.ConversationID, token).Delete(&conversationSnapshotClaim{}).Error
	})
}

func (r *ConversationCompactionRepository) ReleaseSnapshotClaim(ctx context.Context, ownerID, conversationID int64, token, _ string) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ? AND claim_token = ?", ownerID, conversationID, token).Delete(&conversationSnapshotClaim{}).Error
}

func sameNullableID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ conversation.CompactionRepository = (*ConversationCompactionRepository)(nil)
var _ conversation.SnapshotRepository = (*ConversationCompactionRepository)(nil)
