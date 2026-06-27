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
