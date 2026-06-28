package agent

import "context"

type Repository interface {
	Create(ctx context.Context, item *Agent) error
	ListByOwner(ctx context.Context, ownerID int64) ([]Agent, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Agent, error)
	Update(ctx context.Context, item *Agent) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type ProfileRepository interface {
	Create(ctx context.Context, item *Profile) error
	FindByAgent(ctx context.Context, ownerID, agentID int64) (*Profile, error)
	Update(ctx context.Context, item *Profile) error
}

type FlowVersionRepository interface {
	Create(ctx context.Context, item *FlowVersion) error
	ListByAgent(ctx context.Context, ownerID, agentID int64) ([]FlowVersion, error)
	FindByID(ctx context.Context, ownerID, id int64) (*FlowVersion, error)
	FindCurrentByAgent(ctx context.Context, ownerID, agentID int64) (*FlowVersion, error)
	FindLatestByAgent(ctx context.Context, ownerID, agentID int64) (*FlowVersion, error)
	NextVersionNo(ctx context.Context, ownerID, agentID int64) (int, error)
	Publish(ctx context.Context, ownerID, agentID, versionID int64) error
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
	ListDatasetsByAgent(ctx context.Context, ownerID, agentID int64) ([]EvalDataset, error)
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
	CreateCheckpoint(ctx context.Context, item *AgentCheckpoint) error
	FindLatestCheckpointByRun(ctx context.Context, ownerID, runID int64) (*AgentCheckpoint, error)
}

type TeamRepository interface {
	CreateTeam(ctx context.Context, item *Team) error
	FindTeamByID(ctx context.Context, ownerID, id int64) (*Team, error)
	ListTeams(ctx context.Context, ownerID int64) ([]Team, error)
	UpdateTeam(ctx context.Context, item *Team) error
	DeleteTeam(ctx context.Context, ownerID, id int64) error
	AddMember(ctx context.Context, item *TeamMember) error
	RemoveMember(ctx context.Context, ownerID, teamID, agentID int64) error
	ListMembers(ctx context.Context, ownerID, teamID int64) ([]TeamMember, error)
	ListMemberAgentIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error)
}
