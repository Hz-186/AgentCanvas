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
	if job.Status == string(memory.ExtractionCompleted) || job.Status == string(memory.ExtractionFailed) || job.Status == "superseded" {
		job.CompletedAt = &now
	}
	return r.db.WithContext(ctx).Save(job).Error
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

func (r *ExtractionJobRepository) ListPending(ctx context.Context, limit int) ([]memory.ExtractionJob, error) {
	if limit <= 0 {
		limit = 10
	}
	var jobs []memory.ExtractionJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("((status IN ?) AND (due_at IS NULL OR due_at <= ?)) OR (status = ? AND lease_expires_at < ?)", []string{string(memory.ExtractionPending), "analyzed"}, now, memory.ExtractionRunning, now).
			Order("id ASC").Limit(limit).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(jobs))
		for i := range jobs {
			ids = append(ids, jobs[i].ID)
			jobs[i].Status = string(memory.ExtractionRunning)
			jobs[i].AttemptCount++
			lease := now.Add(2 * time.Minute)
			jobs[i].LeaseExpiresAt = &lease
		}
		return tx.Model(&memory.ExtractionJob{}).
			Where("id IN ?", ids).
			Updates(map[string]any{"status": string(memory.ExtractionRunning), "attempt_count": gorm.Expr("attempt_count + 1"), "lease_expires_at": now.Add(2 * time.Minute)}).Error
	})
	return jobs, err
}

type MergeLogRepository struct{ db *gorm.DB }

func NewMergeLogRepository(db *gorm.DB) *MergeLogRepository {
	return &MergeLogRepository{db: db}
}

func (r *MergeLogRepository) Create(ctx context.Context, log *memory.MergeLog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *MergeLogRepository) ListByOwner(ctx context.Context, ownerID int64, limit int) ([]memory.MergeLog, error) {
	var logs []memory.MergeLog
	if limit <= 0 {
		limit = 50
	}
	err := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).
		Order("id DESC").Limit(limit).Find(&logs).Error
	return logs, err
}
