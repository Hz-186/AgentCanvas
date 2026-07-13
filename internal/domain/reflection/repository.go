package reflection

import (
	"context"
	"time"
)

type CandidateQuery struct {
	OwnerID       int64
	WorkflowID    int64
	NodeID        string
	Mode          string
	IncludeGlobal bool
	Limit         int
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
	OwnerID    int64
	WorkflowID int64
	RunID      int64
	NodeID     string
	Mode       string
	Task       string
	Policy     Policy
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
	Task            string
	TaskFingerprint string
	TopK            int
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
