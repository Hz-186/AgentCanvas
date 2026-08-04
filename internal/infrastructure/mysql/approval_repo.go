package mysql

import (
	"context"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ApprovalRepository struct{ db *gorm.DB }

func NewApprovalRepository(db *gorm.DB) *ApprovalRepository { return &ApprovalRepository{db: db} }

func (r *ApprovalRepository) CreateApprovalRequest(ctx context.Context, item *agentdomain.ApprovalRequest) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Status == "" {
		item.Status = agentdomain.ApprovalStatusPending
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindApprovalRequestByID(ctx context.Context, ownerID, id int64) (*agentdomain.ApprovalRequest, error) {
	var item agentdomain.ApprovalRequest
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) FindPendingApprovalByRun(ctx context.Context, ownerID, runID int64) (*agentdomain.ApprovalRequest, error) {
	var item agentdomain.ApprovalRequest
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ? AND status = ?", ownerID, runID, agentdomain.ApprovalStatusPending).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]agentdomain.ApprovalRequest, error) {
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var items []agentdomain.ApprovalRequest
	err := query.Order("id DESC").Find(&items).Error
	return items, err
}

func (r *ApprovalRepository) DecideApprovalAndClaimResume(ctx context.Context, item *agentdomain.ApprovalRequest) error {
	if item == nil {
		return gorm.ErrInvalidData
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current agentdomain.ApprovalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).First(&current).Error; err != nil {
			return err
		}
		if current.Status != agentdomain.ApprovalStatusPending || current.RunID != item.RunID {
			return gorm.ErrInvalidData
		}
		now := time.Now().UTC()
		approvalResult := tx.Model(&agentdomain.ApprovalRequest{}).
			Where("id = ? AND owner_id = ? AND run_id = ? AND status = ?", item.ID, item.OwnerID, item.RunID, agentdomain.ApprovalStatusPending).
			Updates(map[string]any{"status": item.Status, "decision_note": item.DecisionNote, "decided_at": item.DecidedAt, "updated_at": now})
		if approvalResult.Error != nil {
			return approvalResult.Error
		}
		if approvalResult.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		runResult := tx.Model(&agentdomain.Run{}).
			Where("id = ? AND owner_id = ? AND status IN ?", item.RunID, item.OwnerID, []string{agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused}).
			Updates(map[string]any{"status": agentdomain.RunStatusResuming, "updated_at": now})
		if runResult.Error != nil {
			return runResult.Error
		}
		if runResult.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		item.UpdatedAt = now
		return nil
	})
}

func (r *ApprovalRepository) CreateCheckpoint(ctx context.Context, item *agentdomain.RunCheckpoint) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindLatestCheckpointByRun(ctx context.Context, ownerID, runID int64) (*agentdomain.RunCheckpoint, error) {
	var item agentdomain.RunCheckpoint
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) ClaimResume(ctx context.Context, ownerID, runID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run agentdomain.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", runID, ownerID).First(&run).Error; err != nil {
			return err
		}
		if run.Status != agentdomain.RunStatusWaitingHuman && run.Status != agentdomain.RunStatusPaused {
			return gorm.ErrInvalidData
		}
		result := tx.Model(&agentdomain.Run{}).
			Where("id = ? AND owner_id = ? AND status IN ?", runID, ownerID, []string{agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused}).
			Updates(map[string]any{"status": agentdomain.RunStatusResuming, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		return nil
	})
}
