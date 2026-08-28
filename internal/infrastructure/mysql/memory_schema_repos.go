package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/memory"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemoryArtifactRepository struct{ db *gorm.DB }

func NewMemoryArtifactRepository(db *gorm.DB) *MemoryArtifactRepository {
	return &MemoryArtifactRepository{db: db}
}

func (r *MemoryArtifactRepository) Create(ctx context.Context, artifact *memory.MemoryArtifact) error {
	if artifact == nil || artifact.Validate() != nil {
		return gorm.ErrInvalidData
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	artifact.UpdatedAt = artifact.CreatedAt
	return r.db.WithContext(ctx).Create(artifact).Error
}

func (r *MemoryArtifactRepository) Latest(ctx context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error) {
	var item memory.MemoryArtifact
	err := r.db.WithContext(ctx).Where("owner_id = ? AND kind = ?", ownerID, kind).Order("version DESC, id DESC").First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

type MemoryWriteJobRepository struct{ db *gorm.DB }

func NewMemoryWriteJobRepository(db *gorm.DB) *MemoryWriteJobRepository {
	return &MemoryWriteJobRepository{db: db}
}

func (r *MemoryWriteJobRepository) Create(ctx context.Context, job *memory.MemoryWriteJob) error {
	if job == nil {
		return gorm.ErrInvalidData
	}
	if err := job.Validate(); err != nil {
		return err
	}
	if job.Status == "" {
		job.Status = memory.WriteJobStatusPending
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "idempotency_key"}}, DoNothing: true}).Create(job)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return r.db.WithContext(ctx).Where("owner_id = ? AND idempotency_key = ?", job.OwnerID, job.IdempotencyKey).First(job).Error
	}
	return nil
}

func (r *MemoryWriteJobRepository) ClaimPending(ctx context.Context, workerID string, now time.Time, leaseUntil time.Time, limit int) ([]memory.MemoryWriteJob, error) {
	if limit <= 0 {
		limit = 10
	}
	var jobs []memory.MemoryWriteJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if workerID == "" || !leaseUntil.After(now) {
			return gorm.ErrInvalidData
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("(status = ? AND (due_at IS NULL OR due_at <= ?)) OR (status = ? AND lease_expires_at <= ?)", memory.WriteJobStatusPending, now, memory.WriteJobStatusRunning, now).Order("due_at IS NOT NULL ASC, due_at ASC, id ASC").Limit(limit).Find(&jobs).Error; err != nil {
			return err
		}
		for i := range jobs {
			j := &jobs[i]
			ts := now
			j.Status, j.AttemptCount = memory.WriteJobStatusRunning, j.AttemptCount+1
			j.LockedBy, j.LockedAt, j.LeaseExpiresAt, j.UpdatedAt = workerID, &ts, &leaseUntil, now
			if err := tx.Model(j).Updates(map[string]any{"status": j.Status, "attempt_count": j.AttemptCount, "locked_by": workerID, "locked_at": ts, "lease_expires_at": leaseUntil, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return jobs, err
}

func (r *MemoryWriteJobRepository) Update(ctx context.Context, job *memory.MemoryWriteJob) error {
	if job == nil {
		return gorm.ErrInvalidData
	}
	if err := job.Validate(); err != nil {
		return err
	}
	job.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(job).Error
}

var _ memory.MemoryArtifactRepository = (*MemoryArtifactRepository)(nil)
var _ memory.MemoryWriteJobRepository = (*MemoryWriteJobRepository)(nil)
