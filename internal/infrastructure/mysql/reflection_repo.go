package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/reflection"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReflectionRepository struct{ db *gorm.DB }

func NewReflectionRepository(db *gorm.DB) *ReflectionRepository { return &ReflectionRepository{db: db} }

func (r *ReflectionRepository) Create(ctx context.Context, item *reflection.Reflection) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ReflectionRepository) Update(ctx context.Context, item *reflection.Reflection) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ReflectionRepository) FindByID(ctx context.Context, ownerID, id int64) (*reflection.Reflection, error) {
	var item reflection.Reflection
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	return &item, err
}

func (r *ReflectionRepository) FindActiveByHash(ctx context.Context, ownerID, workflowID int64, contentHash string) (*reflection.Reflection, error) {
	var item reflection.Reflection
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND workflow_id = ? AND content_hash = ? AND deleted_at IS NULL", ownerID, workflowID, contentHash).
		Where("status IN ?", []string{reflection.StatusCandidate, reflection.StatusActive, reflection.StatusValidated}).
		First(&item).Error
	return &item, err
}

func (r *ReflectionRepository) ListCandidates(ctx context.Context, q reflection.CandidateQuery) ([]reflection.Reflection, error) {
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	now := time.Now().UTC()
	query := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", q.OwnerID).
		Where("status IN ?", []string{reflection.StatusActive, reflection.StatusValidated}).
		Where("expires_at IS NULL OR expires_at > ?", now)
	if q.IncludeGlobal {
		query = query.Where("workflow_id = ? OR (scope = ? AND status = ?)", q.WorkflowID, reflection.ScopeGlobal, reflection.StatusValidated)
	} else {
		query = query.Where("workflow_id = ?", q.WorkflowID)
	}
	if q.Mode != "" {
		query = query.Where("mode = '' OR mode = ?", q.Mode)
	}
	var items []reflection.Reflection
	err := query.
		Order(clause.Expr{SQL: "CASE WHEN workflow_id = ? AND node_id = ? THEN 0 WHEN workflow_id = ? THEN 1 ELSE 2 END", Vars: []any{q.WorkflowID, q.NodeID, q.WorkflowID}}).
		Order("importance DESC, confidence DESC, successful_use_count DESC, updated_at DESC").
		Limit(limit).Find(&items).Error
	return items, err
}

func (r *ReflectionRepository) ListByWorkflow(ctx context.Context, ownerID, workflowID int64, status string, limit, offset int) ([]reflection.Reflection, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := r.db.WithContext(ctx).Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", ownerID, workflowID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var items []reflection.Reflection
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *ReflectionRepository) MarkRecalled(ctx context.Context, ownerID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&reflection.Reflection{}).
		Where("owner_id = ? AND id IN ? AND deleted_at IS NULL", ownerID, ids).
		Updates(map[string]any{"recall_count": gorm.Expr("recall_count + 1"), "last_recalled_at": now, "updated_at": now}).Error
}

func (r *ReflectionRepository) UpdateUsefulness(ctx context.Context, ownerID, id int64, verdict string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		field := ""
		switch verdict {
		case "helpful":
			field = "successful_use_count"
		case "harmful":
			field = "harmful_count"
		default:
			return nil
		}
		if err := tx.Model(&reflection.Reflection{}).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
			UpdateColumn(field, gorm.Expr(field+" + 1")).Error; err != nil {
			return err
		}
		var item reflection.Reflection
		if err := tx.Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error; err != nil {
			return err
		}
		status := item.Status
		if item.HarmfulCount >= 2 {
			status = reflection.StatusDisputed
		} else if item.SuccessfulUseCount >= 2 && (status == reflection.StatusCandidate || status == reflection.StatusActive) {
			status = reflection.StatusValidated
		}
		if status != item.Status {
			return tx.Model(&reflection.Reflection{}).Where("owner_id = ? AND id = ?", ownerID, id).Update("status", status).Error
		}
		return nil
	})
}

func (r *ReflectionRepository) SetStatus(ctx context.Context, ownerID, id int64, status string) error {
	return r.db.WithContext(ctx).Model(&reflection.Reflection{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()}).Error
}

type ReflectionJobRepository struct{ db *gorm.DB }

func NewReflectionJobRepository(db *gorm.DB) *ReflectionJobRepository {
	return &ReflectionJobRepository{db: db}
}

func (r *ReflectionJobRepository) Create(ctx context.Context, item *reflection.Job) error {
	now := time.Now().UTC()
	if item.Status == "" {
		item.Status = reflection.JobPending
	}
	if item.MaxAttempts <= 0 {
		item.MaxAttempts = 3
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

func (r *ReflectionJobRepository) FindLatestByRun(ctx context.Context, ownerID, runID int64) (*reflection.Job, error) {
	var item reflection.Job
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Order("id DESC").First(&item).Error
	return &item, err
}

func (r *ReflectionJobRepository) ClaimNext(ctx context.Context, workerID string) (*reflection.Job, error) {
	var claimed reflection.Job
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND (retry_at IS NULL OR retry_at <= ?)", reflection.JobPending, now).
			Order("id ASC").First(&claimed).Error; err != nil {
			return err
		}
		claimed.Status = reflection.JobRunning
		claimed.AttemptCount++
		claimed.LockedBy = workerID
		claimed.LockedAt = &now
		claimed.UpdatedAt = now
		return tx.Save(&claimed).Error
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *ReflectionJobRepository) Complete(ctx context.Context, item *reflection.Job) error {
	now := time.Now().UTC()
	item.Status, item.CompletedAt, item.UpdatedAt = reflection.JobCompleted, &now, now
	item.ErrorMessage = ""
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ReflectionJobRepository) Fail(ctx context.Context, item *reflection.Job, cause error, retryAt *time.Time) error {
	item.UpdatedAt = time.Now().UTC()
	if cause != nil {
		item.ErrorMessage = cause.Error()
	}
	if item.AttemptCount >= item.MaxAttempts {
		item.Status = reflection.JobFailed
		item.RetryAt = nil
	} else {
		item.Status = reflection.JobPending
		item.RetryAt = retryAt
	}
	return r.db.WithContext(ctx).Save(item).Error
}

type ReflectionRecallLogRepository struct{ db *gorm.DB }

func NewReflectionRecallLogRepository(db *gorm.DB) *ReflectionRecallLogRepository {
	return &ReflectionRecallLogRepository{db: db}
}

func (r *ReflectionRecallLogRepository) Create(ctx context.Context, item *reflection.RecallLog) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

func (r *ReflectionRecallLogRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]reflection.RecallLog, error) {
	var items []reflection.RecallLog
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("rank ASC, id ASC").Find(&items).Error
	return items, err
}

func (r *ReflectionRecallLogRepository) ResolveRun(ctx context.Context, ownerID, runID int64, outcome string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&reflection.RecallLog{}).Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Updates(map[string]any{"outcome": outcome, "resolved_at": now, "updated_at": now}).Error
}

func (r *ReflectionRecallLogRepository) SetVerdict(ctx context.Context, ownerID, runID, reflectionID int64, verdict, note string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&reflection.RecallLog{}).
		Where("owner_id = ? AND run_id = ? AND reflection_id = ?", ownerID, runID, reflectionID).
		Updates(map[string]any{"verdict": verdict, "feedback_note": note, "resolved_at": now, "updated_at": now}).Error
}

var _ reflection.Repository = (*ReflectionRepository)(nil)
var _ reflection.JobRepository = (*ReflectionJobRepository)(nil)
var _ reflection.RecallLogRepository = (*ReflectionRecallLogRepository)(nil)
