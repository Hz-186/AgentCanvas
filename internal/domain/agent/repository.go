package agent

import (
	"context"
	"errors"
	"time"

	"agentcanvas/internal/domain/conversation"
)

var ErrNoTurnAvailable = errors.New("no agent turn available")
var ErrLeaseLost = errors.New("agent turn worker lease lost")
var ErrNoReviewAvailable = errors.New("no agent improvement review available")

type Repository interface {
	Create(ctx context.Context, item *Agent) error
	ListByOwner(ctx context.Context, ownerID int64) ([]Agent, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Agent, error)
	Update(ctx context.Context, item *Agent) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type TurnRepository interface {
	Create(ctx context.Context, item *Turn) error
	CreateWithArtifacts(ctx context.Context, item *Turn, userMessage *conversation.Message, run *Run) error
	CompleteWithMessage(ctx context.Context, item *Turn, assistantMessage *conversation.Message, run *Run) error
	UpdateRunOwned(ctx context.Context, item *Turn, run *Run, releaseLease bool) error
	FindByID(ctx context.Context, ownerID, id int64) (*Turn, error)
	FindByIdempotencyKey(ctx context.Context, ownerID, conversationID int64, key string) (*Turn, error)
	FindByRunID(ctx context.Context, ownerID, runID int64) (*Turn, error)
	FindLatestByConversation(ctx context.Context, ownerID, agentID, conversationID int64) (*Turn, error)
	Update(ctx context.Context, item *Turn) error
	CancelByRun(ctx context.Context, ownerID, runID int64, finishedAt time.Time) (*Turn, error)
	ClaimNext(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*Turn, error)
	RenewLease(ctx context.Context, turnID int64, leaseToken string, leaseUntil time.Time) error
	ListExpiredRunning(ctx context.Context, before time.Time, limit int) ([]Turn, error)
	RecoverExpired(ctx context.Context, item *Turn, run *Run) error
}

// GoalContinuationStarter atomically checks the persisted Goal/deferral and
// conversation idle state before creating continuation artifacts. It is
// optional so lightweight test repositories can retain the base contract;
// production MySQL uses it to make multi-instance continuation single-shot.
type GoalContinuationStarter interface {
	CreateGoalContinuationWithArtifacts(ctx context.Context, goalID string, item *Turn, userMessage *conversation.Message, run *Run) (bool, error)
}

type RunRepository interface {
	Create(context.Context, *Run) error
	FindByID(context.Context, int64, int64) (*Run, error)
	ListByParent(context.Context, int64, int64) ([]Run, error)
	Update(context.Context, *Run) error
	CancelActive(context.Context, *Run, time.Time) (bool, error)
}

type RunEventRepository interface {
	Create(context.Context, *RunEvent) error
	ListByRun(context.Context, int64, int64) ([]RunEvent, error)
}

type RunStepRepository interface {
	Create(context.Context, *RunStep) error
	ListByRun(context.Context, int64, int64) ([]RunStep, error)
}

type ApprovalRepository interface {
	CreateApprovalRequest(context.Context, *ApprovalRequest) error
	FindApprovalRequestByID(context.Context, int64, int64) (*ApprovalRequest, error)
	FindPendingApprovalByRun(context.Context, int64, int64) (*ApprovalRequest, error)
	ListApprovalRequests(context.Context, int64, string) ([]ApprovalRequest, error)
	DecideApprovalAndClaimResume(context.Context, *ApprovalRequest, []byte) error
	CreateCheckpoint(context.Context, *RunCheckpoint) error
	SavePausedRun(context.Context, *Turn, *Run, *ApprovalRequest, *RunCheckpoint) error
	FindLatestCheckpointByRun(context.Context, int64, int64) (*RunCheckpoint, error)
	ClaimResume(context.Context, int64, int64, []byte) error
}

type ImprovementRepository interface {
	EnqueueReview(ctx context.Context, item *ImprovementReview) error
	ClaimNextReview(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*ImprovementReview, error)
	CompleteReview(ctx context.Context, review *ImprovementReview, proposals []ChangeProposal) error
	FailReview(ctx context.Context, review *ImprovementReview, cause error, retryAt *time.Time) error
	ListReviews(ctx context.Context, ownerID, agentID int64, limit int) ([]ImprovementReview, error)
	ListProposals(ctx context.Context, ownerID, agentID int64, status string, limit int) ([]ChangeProposal, error)
	FindProposal(ctx context.Context, ownerID, id int64) (*ChangeProposal, error)
	UpdateProposal(ctx context.Context, item *ChangeProposal) error
}
