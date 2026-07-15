package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
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
	job.CompletedAt = &now
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
		staleBefore := time.Now().UTC().Add(-10 * time.Minute)
		if err := tx.Where("status = ? OR (status = ? AND created_at < ?)", memory.ExtractionPending, memory.ExtractionRunning, staleBefore).
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
		}
		return tx.Model(&memory.ExtractionJob{}).
			Where("id IN ?", ids).
			Update("status", string(memory.ExtractionRunning)).Error
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
