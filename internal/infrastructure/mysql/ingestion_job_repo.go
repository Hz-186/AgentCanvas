package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IngestionJobRepository struct {
	db *gorm.DB
}

func NewIngestionJobRepository(db *gorm.DB) *IngestionJobRepository {
	return &IngestionJobRepository{db: db}
}

func (r *IngestionJobRepository) Create(ctx context.Context, job *knowledge.IngestionJob) error {
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	return r.db.WithContext(ctx).Create(job).Error
}

func (r *IngestionJobRepository) FindByID(ctx context.Context, ownerID, id int64) (*knowledge.IngestionJob, error) {
	var job knowledge.IngestionJob
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ?", id, ownerID).
		First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *IngestionJobRepository) ClaimNext(ctx context.Context, workerID string) (*knowledge.IngestionJob, error) {
	var claimed *knowledge.IngestionJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job knowledge.IngestionJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND attempt_count < max_attempts", knowledge.IngestionJobStatusPending).
			Order("priority DESC, created_at ASC").
			First(&job).Error; err != nil {
			return err
		}

		now := time.Now().UTC()
		job.Status = knowledge.IngestionJobStatusProcessing
		job.AttemptCount++
		job.LockedBy = workerID
		job.LockedAt = &now
		if job.StartedAt == nil {
			job.StartedAt = &now
		}
		job.UpdatedAt = now
		if err := tx.Save(&job).Error; err != nil {
			return err
		}
		claimed = &job
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *IngestionJobRepository) MarkCompleted(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.IngestionJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      knowledge.IngestionJobStatusCompleted,
			"finished_at": now,
			"updated_at":  now,
		}).Error
}

func (r *IngestionJobRepository) MarkFailed(ctx context.Context, id int64, message string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.IngestionJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        knowledge.IngestionJobStatusFailed,
			"error_message": message,
			"finished_at":   now,
			"updated_at":    now,
		}).Error
}
