package goal

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentcanvas/internal/domain"
)

const (
	StatusActive        = "active"
	StatusPaused        = "paused"
	StatusBlocked       = "blocked"
	StatusUsageLimited  = "usage_limited"
	StatusBudgetLimited = "budget_limited"
	StatusComplete      = "complete"
)

var ErrNotFound = errors.New("goal not found")
var ErrConflict = errors.New("goal conflict")

type ThreadGoal struct {
	domain.BaseModel
	ConversationID  int64  `json:"conversation_id" gorm:"column:conversation_id"`
	GoalID          string `json:"goal_id" gorm:"column:goal_id"`
	Objective       string `json:"objective" gorm:"column:objective"`
	Status          string `json:"status" gorm:"column:status"`
	TokenBudget     *int64 `json:"token_budget,omitempty" gorm:"column:token_budget"`
	TokensUsed      int64  `json:"tokens_used" gorm:"column:tokens_used"`
	TimeUsedSeconds int64  `json:"time_used_seconds" gorm:"column:time_used_seconds"`
}

type ContinuationDeferral struct {
	domain.BaseModel
	ConversationID int64 `json:"conversation_id" gorm:"column:conversation_id;uniqueIndex:ux_goal_deferral"`
}

type ContinuationClaim struct {
	domain.BaseModel
	ConversationID int64     `json:"conversation_id" gorm:"column:conversation_id;uniqueIndex:ux_goal_continuation_claim"`
	GoalID         string    `json:"goal_id" gorm:"column:goal_id"`
	ClaimedAt      time.Time `json:"claimed_at" gorm:"column:claimed_at"`
}

func (ContinuationDeferral) TableName() string { return "agent_thread_goal_deferrals" }
func (ContinuationClaim) TableName() string    { return "agent_thread_goal_claims" }

func (ThreadGoal) TableName() string { return "agent_thread_goals" }

type Repository interface {
	Get(context.Context, int64, int64) (*ThreadGoal, error)
	Create(context.Context, *ThreadGoal) error
	Update(context.Context, *ThreadGoal, string) error
	Delete(context.Context, int64, int64) error
	Account(context.Context, int64, int64, int64, int64, string) (*ThreadGoal, error)
	SetDeferral(context.Context, int64, int64, bool) error
	HasDeferral(context.Context, int64, int64) (bool, error)
}

// VersionedRepository is implemented by stores that can reject stale usage
// writes after a goal replacement without changing the stable Repository API.
type VersionedRepository interface {
	AccountExpected(context.Context, int64, int64, int64, int64, string, string) (*ThreadGoal, error)
}

type ContinuationRepository interface {
	ClaimContinuation(context.Context, int64, int64, string) (bool, error)
	ReleaseContinuation(context.Context, int64, int64, string) error
}

func ValidateObjective(objective string) error {
	if objective == "" {
		return errors.New("goal objective is required")
	}
	if len([]rune(objective)) > 4000 {
		return errors.New("goal objective must be at most 4000 Unicode characters")
	}
	return nil
}

// NormalizeObjective applies Codex's trim-before-validation contract.
func NormalizeObjective(objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if err := ValidateObjective(objective); err != nil {
		return "", err
	}
	return objective, nil
}

func ValidateStatus(status string) bool {
	switch status {
	case StatusActive, StatusPaused, StatusBlocked, StatusUsageLimited, StatusBudgetLimited, StatusComplete:
		return true
	default:
		return false
	}
}

func IsTerminal(status string) bool { return status == StatusBudgetLimited || status == StatusComplete }

func CanSetStatus(current, next string) bool {
	if current == StatusComplete {
		return next == StatusComplete
	}
	if current == StatusBudgetLimited {
		return next == StatusBudgetLimited || next == StatusComplete
	}
	return true
}

func NormalizeBudget(value *int64, ceiling *int64) (*int64, error) {
	if ceiling != nil && *ceiling <= 0 {
		return nil, errors.New("configured goal token budget must be positive")
	}
	if value == nil {
		if ceiling == nil {
			return nil, nil
		}
		v := *ceiling
		return &v, nil
	}
	if *value <= 0 {
		return nil, errors.New("goal token budget must be positive")
	}
	if ceiling != nil && *value > *ceiling {
		return nil, errors.New("goal token budget exceeds configured ceiling")
	}
	return value, nil
}

func (g *ThreadGoal) Touch(now time.Time) { g.UpdatedAt = now }
