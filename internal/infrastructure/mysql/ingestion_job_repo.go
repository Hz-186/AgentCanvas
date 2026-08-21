package mysql

import (
	"context"
	"errors"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const staleIngestionJobLockAfter = 10 * time.Minute

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
		now := time.Now().UTC()
		staleBefore := now.Add(-staleIngestionJobLockAfter)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("((status = ? AND (retry_at IS NULL OR retry_at <= ?)) OR (status = ? AND locked_at IS NOT NULL AND locked_at < ?)) AND (max_attempts <= 0 OR attempt_count < max_attempts)",
				knowledge.IngestionJobStatusPending,
				now,
				knowledge.IngestionJobStatusProcessing,
				staleBefore,
			).
			Order("priority DESC, created_at ASC").
			First(&job).Error; err != nil {
			return err
		}

		job.Status = knowledge.IngestionJobStatusProcessing
		job.AttemptCount++
		job.LockedBy = workerID
		job.LockedAt = &now
		job.RetryAt = nil
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

func (r *IngestionJobRepository) ClaimByID(ctx context.Context, ownerID, id int64, workerID string) (*knowledge.IngestionJob, bool, error) {
	var claimed bool
	var job knowledge.IngestionJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ?", id, ownerID).
			First(&job).Error; err != nil {
			return err
		}
		if job.Status == knowledge.IngestionJobStatusCompleted || job.Status == knowledge.IngestionJobStatusFailed {
			return nil
		}
		now := time.Now().UTC()
		if job.MaxAttempts > 0 && job.AttemptCount >= job.MaxAttempts {
			job.Status = knowledge.IngestionJobStatusFailed
			job.FinishedAt = &now
			job.LockedBy = ""
			job.LockedAt = nil
			job.UpdatedAt = now
			return tx.Save(&job).Error
		}
		staleBefore := now.Add(-staleIngestionJobLockAfter)
		claimable := job.Status == knowledge.IngestionJobStatusPending ||
			(job.Status == knowledge.IngestionJobStatusProcessing && (job.LockedAt == nil || job.LockedAt.Before(staleBefore)))
		if job.Status == knowledge.IngestionJobStatusPending && job.RetryAt != nil && job.RetryAt.After(now) {
			claimable = false
		}
		if !claimable {
			return nil
		}
		job.Status = knowledge.IngestionJobStatusProcessing
		job.AttemptCount++
		job.LockedBy = workerID
		job.LockedAt = &now
		job.RetryAt = nil
		if job.StartedAt == nil {
			job.StartedAt = &now
		}
		job.UpdatedAt = now
		if err := tx.Save(&job).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return &job, claimed, err
}

func (r *IngestionJobRepository) MarkCompleted(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&knowledge.IngestionJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      knowledge.IngestionJobStatusCompleted,
			"locked_by":   "",
			"locked_at":   nil,
			"retry_at":    nil,
			"finished_at": now,
			"updated_at":  now,
		}).Error
}

func (r *IngestionJobRepository) RenewLock(ctx context.Context, id int64, workerID string, lockedAt time.Time) error {
	result := r.db.WithContext(ctx).Model(&knowledge.IngestionJob{}).
		Where("id = ? AND status = ? AND locked_by = ?", id, knowledge.IngestionJobStatusProcessing, workerID).
		Updates(map[string]any{"locked_at": lockedAt.UTC(), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return knowledge.ErrIngestionLeaseLost
	}
	return nil
}

func (r *IngestionJobRepository) MarkCompletedOwned(ctx context.Context, id int64, workerID string) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&knowledge.IngestionJob{}).
		Where("id = ? AND status = ? AND locked_by = ?", id, knowledge.IngestionJobStatusProcessing, workerID).
		Updates(map[string]any{
			"status": knowledge.IngestionJobStatusCompleted, "locked_by": "", "locked_at": nil,
			"retry_at": nil, "finished_at": now, "updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return knowledge.ErrIngestionLeaseLost
	}
	return nil
}

func (r *IngestionJobRepository) MarkFailed(ctx context.Context, id int64, message string) (bool, error) {
	return r.markFailedAt(ctx, id, "", message, time.Time{})
}

func (r *IngestionJobRepository) MarkFailedOwned(ctx context.Context, id int64, workerID, message string) (bool, error) {
	return r.markFailedAt(ctx, id, workerID, message, time.Time{})
}

func (r *IngestionJobRepository) MarkFailedAt(ctx context.Context, id int64, message string, retryAt time.Time) (bool, error) {
	return r.markFailedAt(ctx, id, "", message, retryAt)
}

func (r *IngestionJobRepository) MarkFailedOwnedAt(ctx context.Context, id int64, workerID, message string, retryAt time.Time) (bool, error) {
	return r.markFailedAt(ctx, id, workerID, message, retryAt)
}

func (r *IngestionJobRepository) markFailedAt(ctx context.Context, id int64, workerID, message string, requestedRetryAt time.Time) (bool, error) {
	now := time.Now().UTC()
	final := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job knowledge.IngestionJob
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id)
		if workerID != "" {
			query = query.Where("status = ? AND locked_by = ?", knowledge.IngestionJobStatusProcessing, workerID)
		}
		if err := query.First(&job).Error; err != nil {
			if workerID != "" && errors.Is(err, gorm.ErrRecordNotFound) {
				return knowledge.ErrIngestionLeaseLost
			}
			return err
		}

		maxAttempts := job.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 1
		}
		final = job.AttemptCount >= maxAttempts

		updates := map[string]any{
			"error_message": message,
			"locked_by":     "",
			"locked_at":     nil,
			"updated_at":    now,
		}
		if final {
			updates["status"] = knowledge.IngestionJobStatusFailed
			updates["finished_at"] = now
			updates["retry_at"] = nil
		} else {
			updates["status"] = knowledge.IngestionJobStatusPending
			updates["finished_at"] = nil
			if requestedRetryAt.IsZero() {
				attempt := job.AttemptCount
				if attempt < 1 {
					attempt = 1
				}
				requestedRetryAt = now.Add(time.Second << min(attempt-1, 6))
			}
			updates["retry_at"] = requestedRetryAt.UTC()
		}

		update := tx.Model(&knowledge.IngestionJob{}).Where("id = ?", id)
		if workerID != "" {
			update = update.Where("status = ? AND locked_by = ?", knowledge.IngestionJobStatusProcessing, workerID)
		}
		result := update.Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if workerID != "" && result.RowsAffected != 1 {
			return knowledge.ErrIngestionLeaseLost
		}
		return nil
	})
	return final, err
}

var _ knowledge.ReliableIngestionJobRepository = (*IngestionJobRepository)(nil)
