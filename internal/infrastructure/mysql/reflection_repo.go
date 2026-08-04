package mysql

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/contextresource"
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
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return enqueueReflectionContext(ctx, tx, *item)
	})
}

func (r *ReflectionRepository) Update(ctx context.Context, item *reflection.Reflection) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(item).Error; err != nil {
			return err
		}
		return enqueueReflectionContext(ctx, tx, *item)
	})
}

func (r *ReflectionRepository) FindByID(ctx context.Context, ownerID, id int64) (*reflection.Reflection, error) {
	var item reflection.Reflection
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	return &item, err
}

func (r *ReflectionRepository) FindActiveByHash(ctx context.Context, ownerID, agentID int64, contentHash string) (*reflection.Reflection, error) {
	var item reflection.Reflection
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND agent_id = ? AND content_hash = ? AND deleted_at IS NULL", ownerID, agentID, contentHash).
		Where("status IN ?", []string{reflection.StatusCandidate, reflection.StatusActive, reflection.StatusValidated}).
		First(&item).Error
	return &item, err
}

func (r *ReflectionRepository) FindActiveByAgentHash(ctx context.Context, ownerID, agentID int64, contentHash string) (*reflection.Reflection, error) {
	var item reflection.Reflection
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND agent_id = ? AND content_hash = ? AND deleted_at IS NULL", ownerID, agentID, contentHash).
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
		query = query.Where("agent_id = ? OR (scope = ? AND status = ?)", q.AgentID, reflection.ScopeGlobal, reflection.StatusValidated)
	} else {
		query = query.Where("agent_id = ?", q.AgentID)
	}
	if q.Mode != "" {
		query = query.Where("mode = '' OR mode = ?", q.Mode)
	}
	var items []reflection.Reflection
	err := query.
		Order(clause.Expr{SQL: "CASE WHEN agent_id = ? THEN 0 ELSE 1 END", Vars: []any{q.AgentID}}).
		Order("importance DESC, confidence DESC, successful_use_count DESC, updated_at DESC").
		Limit(limit).Find(&items).Error
	return items, err
}

func (r *ReflectionRepository) ListByAgent(ctx context.Context, ownerID, agentID int64, status string, limit, offset int) ([]reflection.Reflection, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ? AND deleted_at IS NULL", ownerID, agentID)
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
			if err := tx.Model(&reflection.Reflection{}).Where("owner_id = ? AND id = ?", ownerID, id).Update("status", status).Error; err != nil {
				return err
			}
			item.Status = status
		}
		return enqueueReflectionContext(ctx, tx, item)
	})
}

func (r *ReflectionRepository) SetStatus(ctx context.Context, ownerID, id int64, status string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&reflection.Reflection{}).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
			Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		var item reflection.Reflection
		if err := tx.Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error; err != nil {
			return err
		}
		return enqueueReflectionContext(ctx, tx, item)
	})
}

func enqueueReflectionContext(ctx context.Context, tx *gorm.DB, item reflection.Reflection) error {
	operation := contextresource.OperationUpsert
	if item.DeletedAt != nil || (item.Status != reflection.StatusActive && item.Status != reflection.StatusValidated) {
		operation = contextresource.OperationDelete
	}
	return enqueueContextResource(ctx, tx, item.OwnerID, item.AgentID, 0, contextresource.TypeReflection, item.ID, operation, reflectionContextText(item))
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

func (r *ReflectionJobRepository) CreateAndDispatch(ctx context.Context, item *reflection.Job) (*reflection.Job, error) {
	if item == nil {
		return nil, fmt.Errorf("reflection job is required")
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := createReflectionJob(tx, item); err != nil {
			return err
		}
		if item.DispatchSeq <= 0 {
			item.DispatchSeq = 1
			if err := tx.Model(&reflection.Job{}).Where("id = ?", item.ID).Update("dispatch_seq", item.DispatchSeq).Error; err != nil {
				return err
			}
		}
		return createReflectionOutbox(tx, item.ID, item.DispatchSeq, reflection.OutboxJob, time.Now().UTC())
	})
	return item, err
}

func createReflectionJob(tx *gorm.DB, item *reflection.Job) error {
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
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 && item.ID > 0 {
		return nil
	}
	return tx.Where("run_id = ? AND trigger_hash = ?", item.RunID, item.TriggerHash).First(item).Error
}

func createReflectionOutbox(tx *gorm.DB, jobID int64, dispatchSeq int, eventType string, availableAt time.Time) error {
	item := &reflection.JobOutbox{
		EventID: fmt.Sprintf("reflection:%s:%d:%d", eventType, jobID, dispatchSeq), JobID: jobID,
		DispatchSeq: dispatchSeq, EventType: eventType, AvailableAt: availableAt.UTC(), Status: reflection.OutboxPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
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
		staleBefore := now
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? OR (status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)) AND (retry_at IS NULL OR retry_at <= ?) AND (max_attempts <= 0 OR attempt_count < max_attempts)",
				reflection.JobPending, reflection.JobRunning, staleBefore, now).
			Order("id ASC").First(&claimed).Error; err != nil {
			return err
		}
		token, err := newReflectionLockToken()
		if err != nil {
			return err
		}
		leaseUntil := now.Add(3 * time.Minute)
		claimed.Status = reflection.JobRunning
		claimed.AttemptCount++
		claimed.LockedBy = workerID
		claimed.LockedAt = &now
		claimed.LockToken = token
		claimed.LeaseExpiresAt = &leaseUntil
		claimed.LastHeartbeatAt = &now
		claimed.UpdatedAt = now
		return tx.Save(&claimed).Error
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *ReflectionJobRepository) ClaimByID(ctx context.Context, jobID int64, workerID, lockToken string, leaseUntil time.Time) (*reflection.Job, reflection.ClaimState, error) {
	var item reflection.Job
	state := reflection.ClaimBusy
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", jobID).First(&item).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if item.Status == reflection.JobCompleted || item.Status == reflection.JobFailed {
			state = reflection.ClaimTerminal
			return nil
		}
		if item.Status == reflection.JobRunning && item.LeaseExpiresAt != nil && item.LeaseExpiresAt.After(now) {
			state = reflection.ClaimBusy
			return nil
		}
		if item.RetryAt != nil && item.RetryAt.After(now) {
			state = reflection.ClaimBusy
			return nil
		}
		if item.MaxAttempts > 0 && item.AttemptCount >= item.MaxAttempts {
			item.Status = reflection.JobFailed
			item.FailureType = reflection.FailureExhausted
			item.ErrorMessage = "reflection job attempts exhausted before claim"
			item.DispatchSeq++
			item.UpdatedAt = now
			if err := tx.Save(&item).Error; err != nil {
				return err
			}
			if err := createReflectionOutbox(tx, item.ID, item.DispatchSeq, reflection.OutboxDLQ, now); err != nil {
				return err
			}
			state = reflection.ClaimTerminal
			return nil
		}
		item.Status = reflection.JobRunning
		item.AttemptCount++
		item.LockedBy = workerID
		item.LockedAt = &now
		item.LockToken = lockToken
		leaseUntil = leaseUntil.UTC()
		item.LeaseExpiresAt = &leaseUntil
		item.LastHeartbeatAt = &now
		item.UpdatedAt = now
		if err := tx.Save(&item).Error; err != nil {
			return err
		}
		state = reflection.ClaimAcquired
		return nil
	})
	return &item, state, err
}

func (r *ReflectionJobRepository) RenewLease(ctx context.Context, jobID int64, lockToken string, leaseUntil time.Time) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&reflection.Job{}).
		Where("id = ? AND status = ? AND lock_token = ?", jobID, reflection.JobRunning, lockToken).
		Updates(map[string]any{"lease_expires_at": leaseUntil.UTC(), "last_heartbeat_at": now, "updated_at": now})
	return reflectionLeaseResult(result)
}

func (r *ReflectionJobRepository) Complete(ctx context.Context, item *reflection.Job) error {
	now := time.Now().UTC()
	item.Status, item.CompletedAt, item.UpdatedAt = reflection.JobCompleted, &now, now
	item.ErrorMessage = ""
	updates := map[string]any{"status": item.Status, "completed_at": now, "updated_at": now, "error_message": "", "failure_type": "",
		"locked_by": "", "locked_at": nil, "lock_token": "", "lease_expires_at": nil, "last_heartbeat_at": nil}
	query := r.db.WithContext(ctx).Model(&reflection.Job{}).Where("id = ?", item.ID)
	if item.LockToken != "" {
		query = query.Where("status = ? AND lock_token = ?", reflection.JobRunning, item.LockToken)
	}
	return reflectionLeaseResult(query.Updates(updates))
}

func (r *ReflectionJobRepository) Fail(ctx context.Context, item *reflection.Job, cause error, retryAt *time.Time) error {
	now := time.Now().UTC()
	item.UpdatedAt = now
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
	updates := map[string]any{"status": item.Status, "retry_at": item.RetryAt, "error_message": item.ErrorMessage, "updated_at": now,
		"locked_by": "", "locked_at": nil, "lock_token": "", "lease_expires_at": nil, "last_heartbeat_at": nil}
	query := r.db.WithContext(ctx).Model(&reflection.Job{}).Where("id = ?", item.ID)
	if item.LockToken != "" {
		query = query.Where("status = ? AND lock_token = ?", reflection.JobRunning, item.LockToken)
	}
	return reflectionLeaseResult(query.Updates(updates))
}

func (r *ReflectionJobRepository) CommitResult(ctx context.Context, jobID int64, lockToken string, items []reflection.Reflection) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job reflection.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", jobID).First(&job).Error; err != nil {
			return err
		}
		if job.Status != reflection.JobRunning || job.LockToken != lockToken || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(time.Now().UTC()) {
			return reflection.ErrLeaseLost
		}
		now := time.Now().UTC()
		for i := range items {
			item := &items[i]
			if item.ContentHash == "" {
				item.ContentHash = reflectionContentHash(item.RootCauseCategory, item.Lesson, item.CorrectiveAction, item.Applicability)
			}
			if item.CreatedAt.IsZero() {
				item.CreatedAt = now
			}
			item.UpdatedAt = now
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "owner_id"}, {Name: "agent_id"}, {Name: "content_hash"}},
				DoUpdates: clause.Assignments(map[string]any{
					"importance": gorm.Expr("GREATEST(importance, VALUES(importance))"),
					"confidence": gorm.Expr("GREATEST(confidence, VALUES(confidence))"),
					"updated_at": now,
				}),
			}).Create(item).Error; err != nil {
				return err
			}
			var stored reflection.Reflection
			if err := tx.Where("owner_id = ? AND agent_id = ? AND content_hash = ?", item.OwnerID, item.AgentID, item.ContentHash).First(&stored).Error; err != nil {
				return err
			}
			if err := enqueueReflectionContext(ctx, tx, stored); err != nil {
				return err
			}
			jobIDCopy := job.ID
			evidence := &reflection.Evidence{ReflectionID: stored.ID, JobID: &jobIDCopy, RunID: job.RunID,
				CandidateHash: item.ContentHash, EvidenceJSON: item.EvidenceJSON, CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(evidence).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&reflection.Job{}).Where("id = ? AND status = ? AND lock_token = ? AND lease_expires_at > ?", jobID, reflection.JobRunning, lockToken, now).
			Updates(map[string]any{"status": reflection.JobCompleted, "completed_at": now, "updated_at": now, "error_message": "", "failure_type": "",
				"locked_by": "", "locked_at": nil, "lock_token": "", "lease_expires_at": nil, "last_heartbeat_at": nil})
		return reflectionLeaseResult(result)
	})
}

func (r *ReflectionJobRepository) RetryAndDispatch(ctx context.Context, jobID int64, lockToken string, cause error, retryAt time.Time) error {
	return r.finishAndDispatch(ctx, jobID, lockToken, cause, reflection.FailureRetryable, retryAt.UTC(), false, false)
}

func (r *ReflectionJobRepository) FailAndDispatchDLQ(ctx context.Context, jobID int64, lockToken string, cause error, failureType string) error {
	if failureType == "" {
		failureType = reflection.FailurePermanent
	}
	return r.finishAndDispatch(ctx, jobID, lockToken, cause, failureType, time.Now().UTC(), true, false)
}

func (r *ReflectionJobRepository) ReleaseInterrupted(ctx context.Context, jobID int64, lockToken string) error {
	return r.finishAndDispatch(ctx, jobID, lockToken, context.Canceled, "", time.Now().UTC(), false, true)
}

func (r *ReflectionJobRepository) finishAndDispatch(ctx context.Context, jobID int64, lockToken string, cause error, failureType string, availableAt time.Time, terminal, interrupted bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item reflection.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", jobID).First(&item).Error; err != nil {
			return err
		}
		if item.Status != reflection.JobRunning || item.LockToken != lockToken || item.LeaseExpiresAt == nil || !item.LeaseExpiresAt.After(time.Now().UTC()) {
			return reflection.ErrLeaseLost
		}
		if !terminal && !interrupted && item.MaxAttempts > 0 && item.AttemptCount >= item.MaxAttempts {
			terminal = true
			failureType = reflection.FailureExhausted
		}
		now := time.Now().UTC()
		item.DispatchSeq++
		updates := map[string]any{"updated_at": now, "locked_by": "", "locked_at": nil, "lock_token": "", "lease_expires_at": nil, "last_heartbeat_at": nil}
		if cause != nil && !interrupted {
			updates["error_message"] = cause.Error()
		}
		if interrupted {
			updates["status"] = reflection.JobPending
			updates["retry_at"] = nil
			updates["attempt_count"] = gorm.Expr("GREATEST(attempt_count - 1, 0)")
			updates["failure_type"] = ""
		} else if terminal {
			updates["status"] = reflection.JobFailed
			updates["retry_at"] = nil
			updates["failure_type"] = failureType
		} else {
			updates["status"] = reflection.JobPending
			updates["retry_at"] = availableAt
			updates["failure_type"] = failureType
		}
		updates["dispatch_seq"] = item.DispatchSeq
		result := tx.Model(&reflection.Job{}).Where("id = ? AND status = ? AND lock_token = ? AND lease_expires_at > ?", jobID, reflection.JobRunning, lockToken, now).Updates(updates)
		if err := reflectionLeaseResult(result); err != nil {
			return err
		}
		eventType := reflection.OutboxJob
		if terminal {
			eventType = reflection.OutboxDLQ
		}
		return createReflectionOutbox(tx, jobID, item.DispatchSeq, eventType, availableAt)
	})
}

func (r *ReflectionJobRepository) BackfillPendingDispatches(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	var jobs []reflection.Job
	err := r.db.WithContext(ctx).Where("status = ?", reflection.JobPending).
		Where("NOT EXISTS (SELECT 1 FROM agent_reflection_job_outbox o WHERE o.job_id = agent_reflection_jobs.id AND o.event_type = ?)", reflection.OutboxJob).
		Order("id ASC").Limit(limit).Find(&jobs).Error
	if err != nil {
		return 0, err
	}
	var created int64
	for i := range jobs {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			seq := jobs[i].DispatchSeq
			if seq <= 0 {
				seq = 1
				if err := tx.Model(&reflection.Job{}).Where("id = ? AND dispatch_seq = ?", jobs[i].ID, jobs[i].DispatchSeq).Update("dispatch_seq", seq).Error; err != nil {
					return err
				}
			}
			return createReflectionOutbox(tx, jobs[i].ID, seq, reflection.OutboxJob, time.Now().UTC())
		})
		if err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func (r *ReflectionJobRepository) ClaimOutbox(ctx context.Context, workerID string, limit int, lease time.Duration) ([]reflection.JobOutbox, error) {
	if limit <= 0 {
		limit = 100
	}
	if lease <= 0 {
		lease = time.Minute
	}
	var items []reflection.JobOutbox
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		stale := now.Add(-lease)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? OR (status = ? AND locked_at < ?)) AND available_at <= ?", reflection.OutboxPending, reflection.OutboxPublishing, stale, now).
			Order("id ASC").Limit(limit).Find(&items).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].Status = reflection.OutboxPublishing
			items[i].LockedBy = workerID
			items[i].LockedAt = &now
			items[i].UpdatedAt = now
			if err := tx.Save(&items[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}

func (r *ReflectionJobRepository) MarkOutboxPublished(ctx context.Context, id int64, workerID string) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&reflection.JobOutbox{}).
		Where("id = ? AND status = ? AND locked_by = ?", id, reflection.OutboxPublishing, workerID).
		Updates(map[string]any{"status": reflection.OutboxPublished, "published_at": now, "updated_at": now, "locked_by": "", "locked_at": nil, "last_error": ""})
	return reflectionLeaseResult(result)
}

func (r *ReflectionJobRepository) MarkOutboxFailed(ctx context.Context, id int64, workerID string, cause error, next time.Time) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	result := r.db.WithContext(ctx).Model(&reflection.JobOutbox{}).
		Where("id = ? AND status = ? AND locked_by = ?", id, reflection.OutboxPublishing, workerID).
		Updates(map[string]any{"status": reflection.OutboxPending, "available_at": next.UTC(), "attempt_count": gorm.Expr("attempt_count + 1"),
			"last_error": message, "updated_at": time.Now().UTC(), "locked_by": "", "locked_at": nil})
	return reflectionLeaseResult(result)
}

func (r *ReflectionJobRepository) DeletePublishedOutboxBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	result := r.db.WithContext(ctx).Exec("DELETE FROM agent_reflection_job_outbox WHERE status = ? AND published_at < ? ORDER BY id LIMIT ?", reflection.OutboxPublished, cutoff.UTC(), limit)
	return result.RowsAffected, result.Error
}

func reflectionLeaseResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return reflection.ErrLeaseLost
	}
	return nil
}

func newReflectionLockToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func reflectionContentHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
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
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("`rank` ASC, id ASC").Find(&items).Error
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
var _ reflection.ReliableJobRepository = (*ReflectionJobRepository)(nil)
var _ reflection.OutboxRepository = (*ReflectionJobRepository)(nil)
var _ reflection.RecallLogRepository = (*ReflectionRecallLogRepository)(nil)
