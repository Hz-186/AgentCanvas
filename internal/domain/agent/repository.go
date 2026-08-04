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
	CreateRelease(ctx context.Context, item *Release) error
	ListReleases(ctx context.Context, ownerID, agentID int64) ([]Release, error)
	FindReleaseByID(ctx context.Context, ownerID, id int64) (*Release, error)
	NextReleaseVersion(ctx context.Context, ownerID, agentID int64) (int, error)
	SetCurrentRelease(ctx context.Context, ownerID, agentID, releaseID int64) error
}

type TurnRepository interface {
	Create(ctx context.Context, item *Turn) error
	CreateWithArtifacts(ctx context.Context, item *Turn, userMessage *conversation.Message, run *Run) error
	CompleteWithMessage(ctx context.Context, item *Turn, assistantMessage *conversation.Message, run *Run) error
	FindByID(ctx context.Context, ownerID, id int64) (*Turn, error)
	FindByIdempotencyKey(ctx context.Context, ownerID, conversationID int64, key string) (*Turn, error)
	FindByRunID(ctx context.Context, ownerID, runID int64) (*Turn, error)
	FindLatestByConversation(ctx context.Context, ownerID, agentID, conversationID int64) (*Turn, error)
	Update(ctx context.Context, item *Turn) error
	ListQueued(ctx context.Context, limit int) ([]Turn, error)
	ClaimNext(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*Turn, error)
	RenewLease(ctx context.Context, turnID int64, leaseToken string, leaseUntil time.Time) error
	ListExpiredRunning(ctx context.Context, before time.Time, limit int) ([]Turn, error)
	RequeueExpired(ctx context.Context, turnID int64, retryAt time.Time, reason string) error
	PauseExpired(ctx context.Context, turnID int64, reason string) error
}

type RunRepository interface {
	Create(context.Context, *Run) error
	FindByID(context.Context, int64, int64) (*Run, error)
	ListByParent(context.Context, int64, int64) ([]Run, error)
	Update(context.Context, *Run) error
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
	DecideApprovalAndClaimResume(context.Context, *ApprovalRequest) error
	CreateCheckpoint(context.Context, *RunCheckpoint) error
	FindLatestCheckpointByRun(context.Context, int64, int64) (*RunCheckpoint, error)
	ClaimResume(context.Context, int64, int64) error
}

type ImprovementRepository interface {
	EnqueueReview(ctx context.Context, item *ImprovementReview) error
	ClaimNextReview(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*ImprovementReview, error)
	RenewReviewLease(ctx context.Context, reviewID int64, leaseToken string, leaseUntil time.Time) error
	CompleteReview(ctx context.Context, review *ImprovementReview, proposals []ChangeProposal) error
	CreateProposal(ctx context.Context, item *ChangeProposal) error
	FailReview(ctx context.Context, review *ImprovementReview, cause error, retryAt *time.Time) error
	ListReviews(ctx context.Context, ownerID, agentID int64, limit int) ([]ImprovementReview, error)
	ListProposals(ctx context.Context, ownerID, agentID int64, status string, limit int) ([]ChangeProposal, error)
	FindProposal(ctx context.Context, ownerID, id int64) (*ChangeProposal, error)
	UpdateProposal(ctx context.Context, item *ChangeProposal) error
}
