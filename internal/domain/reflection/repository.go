package reflection

import (
	"context"
	"errors"
	"time"
)

var ErrLeaseLost = errors.New("reflection job lease lost")

type ClaimState string

const (
	ClaimAcquired ClaimState = "acquired"
	ClaimBusy     ClaimState = "busy"
	ClaimTerminal ClaimState = "terminal"
)

type CandidateQuery struct {
	OwnerID       int64
	WorkflowID    int64
	AgentID       int64
	NodeID        string
	Mode          string
	IncludeGlobal bool
	Limit         int
}

type ScopedRepository interface {
	FindActiveByAgentHash(context.Context, int64, int64, string) (*Reflection, error)
}

type Repository interface {
	Create(context.Context, *Reflection) error
	Update(context.Context, *Reflection) error
	FindByID(context.Context, int64, int64) (*Reflection, error)
	FindActiveByHash(context.Context, int64, int64, string) (*Reflection, error)
	ListCandidates(context.Context, CandidateQuery) ([]Reflection, error)
	ListByWorkflow(context.Context, int64, int64, string, int, int) ([]Reflection, error)
	MarkRecalled(context.Context, int64, []int64) error
	UpdateUsefulness(context.Context, int64, int64, string) error
	SetStatus(context.Context, int64, int64, string) error
}

type JobRepository interface {
	Create(context.Context, *Job) error
	FindLatestByRun(context.Context, int64, int64) (*Job, error)
	ClaimNext(context.Context, string) (*Job, error)
	Complete(context.Context, *Job) error
	Fail(context.Context, *Job, error, *time.Time) error
}

// ReliableJobRepository adds lease fencing, transactional result persistence,
// and outbox dispatch while retaining JobRepository for the MySQL rollback path.
type ReliableJobRepository interface {
	JobRepository
	CreateAndDispatch(context.Context, *Job) (*Job, error)
	ClaimByID(context.Context, int64, string, string, time.Time) (*Job, ClaimState, error)
	RenewLease(context.Context, int64, string, time.Time) error
	CommitResult(context.Context, int64, string, []Reflection) error
	RetryAndDispatch(context.Context, int64, string, error, time.Time) error
	FailAndDispatchDLQ(context.Context, int64, string, error, string) error
	ReleaseInterrupted(context.Context, int64, string) error
	BackfillPendingDispatches(context.Context, int) (int64, error)
}

type OutboxRepository interface {
	ClaimOutbox(context.Context, string, int, time.Duration) ([]JobOutbox, error)
	MarkOutboxPublished(context.Context, int64, string) error
	MarkOutboxFailed(context.Context, int64, string, error, time.Time) error
	DeletePublishedOutboxBefore(context.Context, time.Time, int) (int64, error)
}

type Envelope struct {
	SchemaVersion  int       `json:"schema_version"`
	EventID        string    `json:"event_id"`
	JobID          int64     `json:"job_id"`
	DispatchSeq    int       `json:"dispatch_seq"`
	DispatchReason string    `json:"dispatch_reason"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type Delivery interface {
	Envelope() Envelope
	ValidationError() error
	Ack(context.Context) error
	Nak(context.Context, time.Duration) error
	InProgress(context.Context) error
	Term(context.Context) error
}

type Transport interface {
	PublishOutbox(context.Context, JobOutbox) error
	Fetch(context.Context, int) ([]Delivery, error)
	PublishDLQ(context.Context, Envelope, string) error
	Drain() error
}

type RecallLogRepository interface {
	Create(context.Context, *RecallLog) error
	ListByRun(context.Context, int64, int64) ([]RecallLog, error)
	ResolveRun(context.Context, int64, int64, string) error
	SetVerdict(context.Context, int64, int64, int64, string, string) error
}

type EventSink interface {
	PublishReflectionEvent(context.Context, Event) error
}

type RecallRequest struct {
	OwnerID             int64
	WorkflowID          int64
	AgentID             int64
	RunID               int64
	NodeID              string
	Mode                string
	Task                string
	EmbeddingProviderID int64
	EmbeddingModel      string
	Policy              Policy
}

type RecalledLesson struct {
	ID               int64   `json:"id"`
	Lesson           string  `json:"lesson"`
	CorrectiveAction string  `json:"corrective_action"`
	Applicability    string  `json:"applicability"`
	Score            float64 `json:"score"`
}

type RecallResult struct {
	Lessons []RecalledLesson `json:"lessons"`
	Context string           `json:"context"`
	Tokens  int              `json:"tokens"`
}

// Advisor is the runtime-facing port. A future event-sourced implementation can
// replace the current storage service without coupling the runner to MySQL.
type Advisor interface {
	Recall(context.Context, RecallRequest) (RecallResult, error)
	Enqueue(context.Context, *Job) error
	ResolveRun(context.Context, int64, int64, string)
	RecordEvaluation(context.Context, int64, int64, bool, string)
}

type SearchRequest struct {
	CandidateQuery
	Task                string
	TaskFingerprint     string
	TopK                int
	EmbeddingProviderID int64
	EmbeddingModel      string
}
type SearchResult struct {
	Reflection Reflection `json:"reflection"`
	Score      float64    `json:"score"`
}
type SearchIndex interface {
	Index(context.Context, Reflection) error
	Search(context.Context, SearchRequest) ([]SearchResult, error)
	Delete(context.Context, int64) error
}
