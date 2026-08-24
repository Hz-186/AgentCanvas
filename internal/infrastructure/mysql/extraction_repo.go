package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ExtractionJobRepository struct{ db *gorm.DB }

func NewExtractionJobRepository(db *gorm.DB) *ExtractionJobRepository {
	return &ExtractionJobRepository{db: db}
}

func (r *ExtractionJobRepository) Create(ctx context.Context, job *memory.ExtractionJob) error {
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(job).Error
}

func (r *ExtractionJobRepository) Update(ctx context.Context, job *memory.ExtractionJob) error {
	now := time.Now().UTC()
	job.UpdatedAt = now
	if job.Status == string(memory.ExtractionCompleted) || job.Status == string(memory.ExtractionFailed) || job.Status == string(memory.ExtractionSuperseded) {
		job.CompletedAt = &now
	}
	return r.db.WithContext(ctx).Save(job).Error
}

func (r *ExtractionJobRepository) ClaimByID(ctx context.Context, ownerID, id int64, workerID string, leaseUntil time.Time) (*memory.ExtractionJob, bool, error) {
	var job memory.ExtractionJob
	claimed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND id = ?", ownerID, id).First(&job).Error; err != nil {
			return err
		}
		if job.Status == string(memory.ExtractionCompleted) || job.Status == string(memory.ExtractionFailed) || job.Status == string(memory.ExtractionSuperseded) {
			return nil
		}
		now := time.Now().UTC()
		if job.Status == string(memory.ExtractionRunning) && job.LockedBy != "" && job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(now) {
			return nil
		}
		if job.AttemptCount >= 5 {
			job.Status = string(memory.ExtractionFailed)
			job.ErrorMessage = "maximum extraction attempts exceeded"
			job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
			return tx.Save(&job).Error
		}
		job.Status = string(memory.ExtractionRunning)
		job.AttemptCount++
		job.LockedBy, job.LockedAt, job.LeaseExpiresAt = workerID, &now, &leaseUntil
		job.UpdatedAt = now
		if err := tx.Save(&job).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return &job, claimed, err
}

func (r *ExtractionJobRepository) RenewLease(ctx context.Context, id int64, workerID string, leaseUntil time.Time) error {
	result := r.db.WithContext(ctx).Model(&memory.ExtractionJob{}).
		Where("id = ? AND locked_by = ? AND status = ?", id, workerID, string(memory.ExtractionRunning)).
		Updates(map[string]any{"lease_expires_at": leaseUntil.UTC(), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return memory.ErrExtractionLeaseLost
	}
	return nil
}

func (r *ExtractionJobRepository) UpdateOwned(ctx context.Context, job *memory.ExtractionJob, workerID string) error {
	if job == nil || workerID == "" {
		return gorm.ErrInvalidData
	}
	now := time.Now().UTC()
	job.UpdatedAt = now
	if job.Status == string(memory.ExtractionCompleted) || job.Status == string(memory.ExtractionFailed) || job.Status == string(memory.ExtractionSuperseded) {
		job.CompletedAt = &now
	}
	updates := map[string]any{
		"status": job.Status, "due_at": job.DueAt, "result_json": job.ResultJSON,
		"error_message": job.ErrorMessage, "completed_at": job.CompletedAt, "updated_at": now,
	}
	if job.Status != string(memory.ExtractionRunning) {
		updates["locked_by"], updates["locked_at"], updates["lease_expires_at"] = "", nil, nil
	}
	result := r.db.WithContext(ctx).Model(&memory.ExtractionJob{}).
		Where("id = ? AND owner_id = ? AND locked_by = ? AND status = ? AND lease_expires_at > ?", job.ID, job.OwnerID, workerID, string(memory.ExtractionRunning), now).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return memory.ErrExtractionLeaseLost
	}
	return nil
}

func (r *ExtractionJobRepository) FindByID(ctx context.Context, ownerID, id int64) (*memory.ExtractionJob, error) {
	var job memory.ExtractionJob
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *ExtractionJobRepository) FindByIdempotencyKey(ctx context.Context, ownerID int64, key string) (*memory.ExtractionJob, error) {
	var job memory.ExtractionJob
	err := r.db.WithContext(ctx).Where("owner_id = ? AND idempotency_key = ?", ownerID, key).First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *ExtractionJobRepository) ListByStatus(ctx context.Context, ownerID int64, status string, limit int) ([]memory.ExtractionJob, error) {
	var jobs []memory.ExtractionJob
	if limit <= 0 {
		limit = 50
	}
	err := r.db.WithContext(ctx).Where("owner_id = ? AND status = ?", ownerID, status).
		Order("id DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

func (r *ExtractionJobRepository) LatestCompletedThrough(ctx context.Context, ownerID, conversationID, beforeJobID int64) (int64, error) {
	var through int64
	query := r.db.WithContext(ctx).Model(&memory.ExtractionJob{}).
		Where("owner_id = ? AND conversation_id = ? AND status = ?", ownerID, conversationID, string(memory.ExtractionCompleted))
	if beforeJobID > 0 {
		query = query.Where("id < ?", beforeJobID)
	}
	err := query.Select("COALESCE(MAX(through_message_id), 0)").Scan(&through).Error
	return through, err
}

func (r *ExtractionJobRepository) ListPending(ctx context.Context, limit int) ([]memory.ExtractionJob, error) {
	if limit <= 0 {
		limit = 10
	}
	var jobs []memory.ExtractionJob
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).
		Where("(status = ? AND (due_at IS NULL OR due_at <= ?)) OR (status = ? AND lease_expires_at < ?)", string(memory.ExtractionPending), now, string(memory.ExtractionRunning), now).
		Order("id ASC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

var _ memory.ExtractionLeaseRepository = (*ExtractionJobRepository)(nil)
