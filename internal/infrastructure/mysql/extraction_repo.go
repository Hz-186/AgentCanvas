package mysql

import (
	"context"
	"strconv"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// durableTriggerReason tags extraction jobs produced by the durable pipeline.
// The conversation-scoped scheduling queries filter on it so non-durable rows
// never influence debounce or window-start decisions.
const durableTriggerReason = "durable"

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

// ListPhase2Retries is intentionally separate from the Phase 1 pending queue:
// a completed extraction with a phase-2 error must be consolidated again
// without re-running rollout extraction.
func (r *ExtractionJobRepository) ListPhase2Retries(ctx context.Context, limit int) ([]memory.ExtractionJob, error) {
	if limit <= 0 {
		limit = 20
	}
	var jobs []memory.ExtractionJob
	err := r.db.WithContext(ctx).
		Where("status = ? AND trigger_reason = ? AND error_message LIKE ? AND (due_at IS NULL OR due_at <= ?)", string(memory.ExtractionCompleted), "durable", "phase2:%", time.Now().UTC()).
		Order("id ASC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

// LatestDurableJob returns the conversation's newest durable extraction job
// (MAX(id), any status, any idempotency-key generation) or (nil, nil) when
// the conversation has none. It is the scheduling-decision read for
// repositories that compose the debounce outside a single transaction.
func (r *ExtractionJobRepository) LatestDurableJob(ctx context.Context, ownerID, conversationID int64) (*memory.ExtractionJob, error) {
	var jobs []memory.ExtractionJob
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND conversation_id = ? AND trigger_reason = ?", ownerID, conversationID, durableTriggerReason).
		Order("id DESC").Limit(1).Find(&jobs).Error
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	return &jobs[0], nil
}

// RefreshPendingBoundary updates a still-pending job's boundary in place and
// reports whether the row was refreshed. The conditional status guard makes
// the refresh a no-op once a worker has claimed the row, so a concurrent claim
// surfaces as refreshed=false instead of a corrupted boundary.
func (r *ExtractionJobRepository) RefreshPendingBoundary(ctx context.Context, ownerID, jobID, throughMessageID int64, dueAt time.Time) (bool, error) {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&memory.ExtractionJob{}).
		Where("id = ? AND owner_id = ? AND status = ?", jobID, ownerID, string(memory.ExtractionPending)).
		Updates(map[string]any{"through_message_id": throughMessageID, "due_at": dueAt, "updated_at": now})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// LatestCompletedDurableThrough returns the through_message_id of the
// conversation's latest (MAX(id)) completed durable job, or 0 when none
// exists. It replaces the retired bounded ListByStatus scan as the extraction
// window-start lookup and uses idx_conversation_id.
func (r *ExtractionJobRepository) LatestCompletedDurableThrough(ctx context.Context, ownerID, conversationID int64) (int64, error) {
	var through int64
	err := r.db.WithContext(ctx).Model(&memory.ExtractionJob{}).
		Select("through_message_id").
		Where("owner_id = ? AND conversation_id = ? AND trigger_reason = ? AND status = ?", ownerID, conversationID, durableTriggerReason, string(memory.ExtractionCompleted)).
		Order("id DESC").Limit(1).Scan(&through).Error
	return through, err
}

// ScheduleDurableBoundary implements the session-level debounce as a single
// transaction. It begins with a FOR UPDATE locking read scoped to the
// conversation's latest durable row only (never a table-wide lock), then
// branches: a pending latest row is refreshed in place; a running latest row
// receives exactly one successor; a terminal or absent latest row receives a
// fresh row. A unique-key conflict re-reads and returns the existing row. The
// boolean reports whether a new row was created (queue wakeups attach only to
// creations).
func (r *ExtractionJobRepository) ScheduleDurableBoundary(ctx context.Context, ownerID, conversationID, throughMessageID int64, dueAt time.Time) (*memory.ExtractionJob, bool, error) {
	var scheduled *memory.ExtractionJob
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scheduled = nil
		created = false
		var latest []memory.ExtractionJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("owner_id = ? AND conversation_id = ? AND trigger_reason = ?", ownerID, conversationID, durableTriggerReason).
			Order("id DESC").Limit(1).Find(&latest).Error; err != nil {
			return err
		}
		if len(latest) > 0 && latest[0].Status == string(memory.ExtractionPending) {
			pending := latest[0]
			now := time.Now().UTC()
			result := tx.Model(&memory.ExtractionJob{}).
				Where("id = ? AND owner_id = ? AND status = ?", pending.ID, ownerID, string(memory.ExtractionPending)).
				Updates(map[string]any{"through_message_id": throughMessageID, "due_at": dueAt, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				pending.ThroughMessageID = throughMessageID
				pending.DueAt = &dueAt
				scheduled = &pending
				return nil
			}
			// Defensive fallback: the pending row was claimed between the
			// locking read and the conditional update. Fall through and create
			// its successor.
		}
		row := newBoundaryRow(ownerID, conversationID, throughMessageID, dueAt, latest)
		if err := tx.Create(row).Error; err != nil {
			var existing memory.ExtractionJob
			if findErr := tx.Where("owner_id = ? AND idempotency_key = ?", ownerID, row.IdempotencyKey).First(&existing).Error; findErr == nil {
				scheduled = &existing
				return nil
			}
			return err
		}
		scheduled = row
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return scheduled, created, nil
}

// newBoundaryRow builds the row to insert for a boundary schedule: the first
// durable job of a conversation carries the :initial key; every later job is a
// successor keyed after the latest job it extends.
func newBoundaryRow(ownerID, conversationID, throughMessageID int64, dueAt time.Time, latest []memory.ExtractionJob) *memory.ExtractionJob {
	key := durableInitialKey(ownerID, conversationID)
	if len(latest) > 0 {
		key = durableSuccessorKey(ownerID, conversationID, latest[0].ID)
	}
	due := dueAt
	return &memory.ExtractionJob{
		BaseModel:        domain.BaseModel{OwnerID: ownerID},
		ConversationID:   conversationID,
		IdempotencyKey:   key,
		TriggerReason:    durableTriggerReason,
		ThroughMessageID: throughMessageID,
		Status:           string(memory.ExtractionPending),
		DueAt:            &due,
	}
}

func durableInitialKey(ownerID, conversationID int64) string {
	return "durable:" + strconv.FormatInt(ownerID, 10) + ":" + strconv.FormatInt(conversationID, 10) + ":initial"
}

func durableSuccessorKey(ownerID, conversationID, jobID int64) string {
	return "durable:" + strconv.FormatInt(ownerID, 10) + ":" + strconv.FormatInt(conversationID, 10) + ":after-job:" + strconv.FormatInt(jobID, 10)
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
