package agent

import (
	"context"
	"errors"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/workflow"
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
	CreateWithArtifacts(ctx context.Context, item *Turn, userMessage *conversation.Message, run *workflow.Run) error
	CompleteWithMessage(ctx context.Context, item *Turn, assistantMessage *conversation.Message, run *workflow.Run) error
	FindByID(ctx context.Context, ownerID, id int64) (*Turn, error)
	FindByIdempotencyKey(ctx context.Context, ownerID, conversationID int64, key string) (*Turn, error)
	FindByRunID(ctx context.Context, ownerID, runID int64) (*Turn, error)
	Update(ctx context.Context, item *Turn) error
	ListQueued(ctx context.Context, limit int) ([]Turn, error)
	ClaimNext(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*Turn, error)
	RenewLease(ctx context.Context, turnID int64, leaseToken string, leaseUntil time.Time) error
	ListExpiredRunning(ctx context.Context, before time.Time, limit int) ([]Turn, error)
	RequeueExpired(ctx context.Context, turnID int64, retryAt time.Time, reason string) error
	PauseExpired(ctx context.Context, turnID int64, reason string) error
}

type ImprovementRepository interface {
	EnqueueReview(ctx context.Context, item *ImprovementReview) error
	ClaimNextReview(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*ImprovementReview, error)
	RenewReviewLease(ctx context.Context, reviewID int64, leaseToken string, leaseUntil time.Time) error
	CompleteReview(ctx context.Context, review *ImprovementReview, proposals []ChangeProposal) error
	FailReview(ctx context.Context, review *ImprovementReview, cause error, retryAt *time.Time) error
	ListReviews(ctx context.Context, ownerID, agentID int64, limit int) ([]ImprovementReview, error)
	ListProposals(ctx context.Context, ownerID, agentID int64, status string, limit int) ([]ChangeProposal, error)
	FindProposal(ctx context.Context, ownerID, id int64) (*ChangeProposal, error)
	UpdateProposal(ctx context.Context, item *ChangeProposal) error
}
