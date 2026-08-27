package mysql

import (
	"context"
	"errors"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/goal"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GoalRepository struct{ db *gorm.DB }

func NewGoalRepository(db *gorm.DB) *GoalRepository { return &GoalRepository{db: db} }

func (r *GoalRepository) Get(ctx context.Context, ownerID, conversationID int64) (*goal.ThreadGoal, error) {
	var item goal.ThreadGoal
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, goal.ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *GoalRepository) Create(ctx context.Context, item *goal.ThreadGoal) error {
	if item == nil {
		return gorm.ErrInvalidData
	}
	if item.GoalID == "" {
		item.GoalID = uuid.NewString()
	}
	if item.Status == "" {
		item.Status = goal.StatusActive
	}
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *GoalRepository) Update(ctx context.Context, item *goal.ThreadGoal, expectedGoalID string) error {
	if item == nil {
		return gorm.ErrInvalidData
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current goal.ThreadGoal
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND conversation_id = ?", item.OwnerID, item.ConversationID)
		if expectedGoalID != "" {
			query = query.Where("goal_id = ?", expectedGoalID)
		}
		if err := query.First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return goal.ErrConflict
			}
			return err
		}
		if !goal.CanSetStatus(current.Status, item.Status) {
			return goal.ErrConflict
		}
		now := time.Now().UTC()
		if err := tx.Model(&goal.ThreadGoal{}).Where("id = ?", current.ID).Updates(map[string]any{"objective": item.Objective, "status": item.Status, "token_budget": item.TokenBudget, "updated_at": now}).Error; err != nil {
			return err
		}
		item.UpdatedAt = now
		return nil
	})
}

func (r *GoalRepository) Delete(ctx context.Context, ownerID, conversationID int64) error {
	result := r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).Delete(&goal.ThreadGoal{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return goal.ErrNotFound
	}
	return nil
}

func (r *GoalRepository) Account(ctx context.Context, ownerID, conversationID, timeDelta, tokenDelta int64, mode string) (*goal.ThreadGoal, error) {
	return r.account(ctx, ownerID, conversationID, timeDelta, tokenDelta, mode, "")
}

func (r *GoalRepository) AccountExpected(ctx context.Context, ownerID, conversationID, timeDelta, tokenDelta int64, mode, expectedGoalID string) (*goal.ThreadGoal, error) {
	return r.account(ctx, ownerID, conversationID, timeDelta, tokenDelta, mode, expectedGoalID)
}

func (r *GoalRepository) account(ctx context.Context, ownerID, conversationID, timeDelta, tokenDelta int64, mode, expectedGoalID string) (*goal.ThreadGoal, error) {
	if timeDelta < 0 {
		timeDelta = 0
	}
	if tokenDelta < 0 {
		tokenDelta = 0
	}
	returned := &goal.ThreadGoal{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current goal.ThreadGoal
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).First(&current).Error; err != nil {
			return err
		}
		if expectedGoalID != "" && current.GoalID != expectedGoalID {
			*returned = current
			return nil
		}
		allowed := current.Status == goal.StatusActive || current.Status == goal.StatusBudgetLimited
		if mode == "active_or_complete" {
			allowed = current.Status == goal.StatusActive || current.Status == goal.StatusBudgetLimited || current.Status == goal.StatusComplete
		}
		if mode == "active_or_stopped" {
			allowed = current.Status == goal.StatusActive || current.Status == goal.StatusPaused || current.Status == goal.StatusBlocked || current.Status == goal.StatusUsageLimited || current.Status == goal.StatusBudgetLimited
		}
		if !allowed || (timeDelta == 0 && tokenDelta == 0) {
			*returned = current
			return nil
		}
		current.TimeUsedSeconds += timeDelta
		current.TokensUsed += tokenDelta
		if current.Status == goal.StatusActive && current.TokenBudget != nil && current.TokensUsed >= *current.TokenBudget {
			current.Status = goal.StatusBudgetLimited
		}
		current.UpdatedAt = time.Now().UTC()
		if err := tx.Model(&goal.ThreadGoal{}).Where("id = ?", current.ID).Updates(map[string]any{"time_used_seconds": current.TimeUsedSeconds, "tokens_used": current.TokensUsed, "status": current.Status, "updated_at": current.UpdatedAt}).Error; err != nil {
			return err
		}
		*returned = current
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, goal.ErrNotFound
	}
	return returned, err
}

func (r *GoalRepository) SetDeferral(ctx context.Context, ownerID, conversationID int64, deferred bool) error {
	var row goal.ContinuationDeferral
	query := r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID)
	if !deferred {
		return query.Delete(&row).Error
	}
	return r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).FirstOrCreate(&row, &goal.ContinuationDeferral{BaseModel: domain.BaseModel{OwnerID: ownerID}, ConversationID: conversationID}).Error
}

func (r *GoalRepository) HasDeferral(ctx context.Context, ownerID, conversationID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&goal.ContinuationDeferral{}).Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).Count(&count).Error
	return count > 0, err
}
