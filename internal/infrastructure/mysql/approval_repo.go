package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ApprovalRepository struct{ db *gorm.DB }

func NewApprovalRepository(db *gorm.DB) *ApprovalRepository { return &ApprovalRepository{db: db} }

func (r *ApprovalRepository) CreateApprovalRequest(ctx context.Context, item *workflow.ApprovalRequest) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Status == "" {
		item.Status = workflow.ApprovalStatusPending
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindApprovalRequestByID(ctx context.Context, ownerID, id int64) (*workflow.ApprovalRequest, error) {
	var item workflow.ApprovalRequest
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) FindPendingApprovalByRun(ctx context.Context, ownerID, runID int64) (*workflow.ApprovalRequest, error) {
	var item workflow.ApprovalRequest
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ? AND status = ?", ownerID, runID, workflow.ApprovalStatusPending).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]workflow.ApprovalRequest, error) {
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var items []workflow.ApprovalRequest
	err := query.Order("id DESC").Find(&items).Error
	return items, err
}

func (r *ApprovalRepository) UpdateApprovalRequest(ctx context.Context, item *workflow.ApprovalRequest) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ApprovalRepository) CreateCheckpoint(ctx context.Context, item *workflow.WorkflowCheckpoint) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindLatestCheckpointByRun(ctx context.Context, ownerID, runID int64) (*workflow.WorkflowCheckpoint, error) {
	var item workflow.WorkflowCheckpoint
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
		var run workflow.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", runID, ownerID).First(&run).Error; err != nil {
			return err
		}
		if run.Status != workflow.RunStatusWaitingHuman && run.Status != workflow.RunStatusPaused {
			return gorm.ErrInvalidData
		}
		result := tx.Model(&workflow.Run{}).
			Where("id = ? AND owner_id = ? AND status IN ?", runID, ownerID, []string{workflow.RunStatusWaitingHuman, workflow.RunStatusPaused}).
			Updates(map[string]any{"status": workflow.RunStatusResuming, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		return nil
	})
}
