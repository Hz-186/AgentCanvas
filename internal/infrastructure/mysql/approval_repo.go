package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/runtime/toolruntime"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ApprovalRepository struct{ db *gorm.DB }

func NewApprovalRepository(db *gorm.DB) *ApprovalRepository { return &ApprovalRepository{db: db} }

func (r *ApprovalRepository) CreateApprovalRequest(ctx context.Context, item *agentdomain.ApprovalRequest) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Status == "" {
		item.Status = agentdomain.ApprovalStatusPending
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) FindApprovalRequestByID(ctx context.Context, ownerID, id int64) (*agentdomain.ApprovalRequest, error) {
	var item agentdomain.ApprovalRequest
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	hydrateApproval(&item)
	return &item, nil
}

func (r *ApprovalRepository) FindPendingApprovalByRun(ctx context.Context, ownerID, runID int64) (*agentdomain.ApprovalRequest, error) {
	var item agentdomain.ApprovalRequest
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ? AND status = ?", ownerID, runID, agentdomain.ApprovalStatusPending).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	hydrateApproval(&item)
	return &item, nil
}

func (r *ApprovalRepository) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]agentdomain.ApprovalRequest, error) {
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var items []agentdomain.ApprovalRequest
	err := query.Order("id DESC").Find(&items).Error
	for i := range items {
		hydrateApproval(&items[i])
	}
	return items, err
}

func hydrateApproval(item *agentdomain.ApprovalRequest) {
	if item == nil || len(item.RequestJSON) == 0 {
		return
	}
	var approval struct {
		Options   []toolruntime.ApprovalOption    `json:"options"`
		Questions []toolruntime.UserInputQuestion `json:"questions"`
	}
	if json.Unmarshal(item.RequestJSON, &approval) == nil {
		item.Questions = approval.Questions
	}
}

func (r *ApprovalRepository) DecideApprovalAndClaimResume(ctx context.Context, item *agentdomain.ApprovalRequest, turnInput []byte) error {
	if item == nil {
		return gorm.ErrInvalidData
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current agentdomain.ApprovalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).First(&current).Error; err != nil {
			return err
		}
		if current.Status != agentdomain.ApprovalStatusPending || current.RunID != item.RunID {
			return gorm.ErrInvalidData
		}
		var run agentdomain.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", item.RunID, item.OwnerID).First(&run).Error; err != nil {
			return err
		}
		if run.Status != agentdomain.RunStatusWaitingHuman && run.Status != agentdomain.RunStatusPaused {
			return gorm.ErrInvalidData
		}
		now := time.Now().UTC()
		approvalResult := tx.Model(&agentdomain.ApprovalRequest{}).
			Where("id = ? AND owner_id = ? AND run_id = ? AND status = ?", item.ID, item.OwnerID, item.RunID, agentdomain.ApprovalStatusPending).
			Updates(map[string]any{"status": item.Status, "decision_note": item.DecisionNote, "decided_at": item.DecidedAt, "updated_at": now})
		if approvalResult.Error != nil {
			return approvalResult.Error
		}
		if approvalResult.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		runResult := tx.Model(&agentdomain.Run{}).
			Where("id = ? AND owner_id = ? AND status IN ?", item.RunID, item.OwnerID, []string{agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused}).
			Updates(map[string]any{"status": agentdomain.RunStatusResuming, "updated_at": now})
		if runResult.Error != nil {
			return runResult.Error
		}
		if runResult.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		if err := queueResumeTurn(tx, &run, turnInput, now); err != nil {
			return err
		}
		item.UpdatedAt = now
		return nil
	})
}

func (r *ApprovalRepository) CreateCheckpoint(ctx context.Context, item *agentdomain.RunCheckpoint) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ApprovalRepository) SavePausedRun(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, approval *agentdomain.ApprovalRequest, checkpoint *agentdomain.RunCheckpoint) error {
	if turn == nil || run == nil || checkpoint == nil || turn.LeaseToken == "" {
		return gorm.ErrInvalidData
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ownedTurn agentdomain.Turn
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND lease_token = ? AND status = ?", turn.ID, turn.OwnerID, turn.LeaseToken, agentdomain.TurnStatusRunning).
			First(&ownedTurn).Error; err != nil {
			return err
		}
		var ownedRun agentdomain.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND status IN ?", run.ID, run.OwnerID, []string{agentdomain.RunStatusRunning, agentdomain.RunStatusResuming}).
			First(&ownedRun).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		turn.UpdatedAt, run.UpdatedAt = now, now
		clearTurnLease(turn)
		if err := tx.Save(turn).Error; err != nil {
			return err
		}
		if err := tx.Save(run).Error; err != nil {
			return err
		}
		if approval != nil {
			approval.CreatedAt, approval.UpdatedAt = now, now
			if approval.Status == "" {
				approval.Status = agentdomain.ApprovalStatusPending
			}
			if err := tx.Create(approval).Error; err != nil {
				return err
			}
		}
		checkpoint.CreatedAt = now
		return tx.Create(checkpoint).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agentdomain.ErrLeaseLost
	}
	return err
}

func (r *ApprovalRepository) FindLatestCheckpointByRun(ctx context.Context, ownerID, runID int64) (*agentdomain.RunCheckpoint, error) {
	var item agentdomain.RunCheckpoint
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND run_id = ?", ownerID, runID).
		Order("id DESC").
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ApprovalRepository) ClaimResume(ctx context.Context, ownerID, runID int64, turnInput []byte) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run agentdomain.Run
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", runID, ownerID).First(&run).Error; err != nil {
			return err
		}
		if run.Status != agentdomain.RunStatusWaitingHuman && run.Status != agentdomain.RunStatusPaused {
			return gorm.ErrInvalidData
		}
		result := tx.Model(&agentdomain.Run{}).
			Where("id = ? AND owner_id = ? AND status IN ?", runID, ownerID, []string{agentdomain.RunStatusWaitingHuman, agentdomain.RunStatusPaused}).
			Updates(map[string]any{"status": agentdomain.RunStatusResuming, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		return queueResumeTurn(tx, &run, turnInput, time.Now().UTC())
	})
}

func queueResumeTurn(tx *gorm.DB, run *agentdomain.Run, input []byte, now time.Time) error {
	if run == nil {
		return gorm.ErrInvalidData
	}
	if len(input) == 0 || !json.Valid(input) {
		return gorm.ErrInvalidData
	}
	result := tx.Model(&agentdomain.Turn{}).
		Where("owner_id = ? AND run_id = ? AND status IN ?", run.OwnerID, run.ID, []string{agentdomain.TurnStatusPaused, agentdomain.TurnStatusWaitingHuman}).
		Updates(map[string]any{
			"status": agentdomain.TurnStatusQueued, "input_json": input, "error_message": "", "finished_at": nil, "retry_at": nil,
			"worker_id": "", "lease_token": "", "lease_expires_at": nil, "last_heartbeat_at": nil, "updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		if run.RunType != agentdomain.RunTypeSubagent {
			return gorm.ErrInvalidData
		}
		conversationID := int64(0)
		if run.ConversationID != nil {
			conversationID = *run.ConversationID
		}
		return tx.Create(&agentdomain.Turn{
			BaseModel: domain.BaseModel{OwnerID: run.OwnerID, CreatedAt: now, UpdatedAt: now}, AgentID: run.AgentID, ConversationID: conversationID,
			RunID: &run.ID, IdempotencyKey: "subagent-resume-" + strconv.FormatInt(run.ID, 10), Status: agentdomain.TurnStatusQueued,
			InputJSON: input, MaxAttempts: 3,
		}).Error
	}
	return nil
}
