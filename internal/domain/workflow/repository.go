package workflow

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRuleSetConflict  = errors.New("rule set revision conflict")
	ErrRuleSetImmutable = errors.New("published rule set is immutable")
	ErrRuleCompileStale = errors.New("rule compile job is stale")
)

type Repository interface {
	Create(ctx context.Context, item *Workflow) error
	ListByOwner(ctx context.Context, ownerID int64) ([]Workflow, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Workflow, error)
	Update(ctx context.Context, item *Workflow) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type ProfileRepository interface {
	Create(ctx context.Context, item *Profile) error
	FindByWorkflow(ctx context.Context, ownerID, workflowID int64) (*Profile, error)
	Update(ctx context.Context, item *Profile) error
}

type RuleSetRepository interface {
	CreateDraft(ctx context.Context, item *RuleSet, nodes []RuleNode, edges []RuleEdge) error
	ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]RuleSet, error)
	FindByID(ctx context.Context, ownerID, workflowID, id int64) (*RuleSet, error)
	UpdateDraft(ctx context.Context, item *RuleSet, nodes []RuleNode, edges []RuleEdge, expectedRevision int64) error
	QueueCompilation(ctx context.Context, item *RuleSet, job *RuleCompileJob, expectedRevision int64) error
	FindCompileJob(ctx context.Context, ownerID, workflowID, id int64) (*RuleCompileJob, error)
	ClaimCompileJob(ctx context.Context, jobID int64, workerID string) (*RuleCompileJob, error)
	ClaimNextCompileJob(ctx context.Context, workerID string) (*RuleCompileJob, error)
	CompleteCompilation(ctx context.Context, job *RuleCompileJob, nodes []RuleNode, suggestions []RuleEdge, compiledSnapshot []byte, compiledHash, tokenEstimator, nextStatus string) error
	FailCompilation(ctx context.Context, job *RuleCompileJob, cause error, retryAt *time.Time) error
	UpdateEdgeDecisions(ctx context.Context, ownerID, workflowID, ruleSetID, expectedRevision int64, decisions map[int64]string) (*RuleSet, error)
	PublishCompiled(ctx context.Context, item *RuleSet, nodes []RuleNode, edges []RuleEdge, compiledSnapshot []byte, compiledHash, tokenEstimator string, publishedBy int64) error
	RollbackPublished(ctx context.Context, target *RuleSet, clone *RuleSet, publishedBy int64, compile RuleSetRollbackCompiler) error
}

type RuleSetRollbackCompiler func(ruleSetID int64, versionNo int) (nodes []RuleNode, edges []RuleEdge, snapshot []byte, compiledHash, tokenEstimator string, err error)

type WorkflowVersionRepository interface {
	Create(ctx context.Context, item *WorkflowVersion) error
	ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]WorkflowVersion, error)
	FindByID(ctx context.Context, ownerID, id int64) (*WorkflowVersion, error)
	FindCurrentByWorkflow(ctx context.Context, ownerID, workflowID int64) (*WorkflowVersion, error)
	FindLatestByWorkflow(ctx context.Context, ownerID, workflowID int64) (*WorkflowVersion, error)
	NextVersionNo(ctx context.Context, ownerID, workflowID int64) (int, error)
	Publish(ctx context.Context, ownerID, workflowID, versionID int64) error
}

type RunRepository interface {
	Create(ctx context.Context, item *Run) error
	FindByID(ctx context.Context, ownerID, id int64) (*Run, error)
	ListByParent(ctx context.Context, ownerID, parentRunID int64) ([]Run, error)
	Update(ctx context.Context, item *Run) error
}

type RunEventRepository interface {
	Create(ctx context.Context, item *RunEvent) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]RunEvent, error)
}

type NodeLogRepository interface {
	Create(ctx context.Context, item *NodeLog) error
	Update(ctx context.Context, item *NodeLog) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]NodeLog, error)
}

type RunStepRepository interface {
	Create(ctx context.Context, item *RunStep) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]RunStep, error)
}

type EvalDatasetRepository interface {
	CreateDataset(ctx context.Context, item *EvalDataset) error
	ListDatasetsByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]EvalDataset, error)
	FindDatasetByID(ctx context.Context, ownerID, id int64) (*EvalDataset, error)
	CreateCase(ctx context.Context, item *EvalCase) error
	ListCasesByDataset(ctx context.Context, ownerID, datasetID int64) ([]EvalCase, error)
	CreateEvalRun(ctx context.Context, item *EvalRun) error
	UpdateEvalRun(ctx context.Context, item *EvalRun) error
	FindEvalRunByID(ctx context.Context, ownerID, id int64) (*EvalRun, error)
	ListEvalRunsByDataset(ctx context.Context, ownerID, datasetID int64) ([]EvalRun, error)
	CreateEvalResult(ctx context.Context, item *EvalResult) error
	ListEvalResultsByRun(ctx context.Context, ownerID, evalRunID int64) ([]EvalResult, error)
}

type ApprovalRepository interface {
	CreateApprovalRequest(ctx context.Context, item *ApprovalRequest) error
	FindApprovalRequestByID(ctx context.Context, ownerID, id int64) (*ApprovalRequest, error)
	FindPendingApprovalByRun(ctx context.Context, ownerID, runID int64) (*ApprovalRequest, error)
	ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]ApprovalRequest, error)
	UpdateApprovalRequest(ctx context.Context, item *ApprovalRequest) error
	CreateCheckpoint(ctx context.Context, item *WorkflowCheckpoint) error
	FindLatestCheckpointByRun(ctx context.Context, ownerID, runID int64) (*WorkflowCheckpoint, error)
}

type TeamRepository interface {
	CreateTeam(ctx context.Context, item *Team) error
	FindTeamByID(ctx context.Context, ownerID, id int64) (*Team, error)
	ListTeams(ctx context.Context, ownerID int64) ([]Team, error)
	UpdateTeam(ctx context.Context, item *Team) error
	DeleteTeam(ctx context.Context, ownerID, id int64) error
	AddMember(ctx context.Context, item *TeamMember) error
	RemoveMember(ctx context.Context, ownerID, teamID, workflowID int64) error
	ListMembers(ctx context.Context, ownerID, teamID int64) ([]TeamMember, error)
	ListMemberWorkflowIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error)
}
