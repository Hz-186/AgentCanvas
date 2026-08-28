package event

import "time"

const (
	AgentStarted           = "agent_started"
	AgentStep              = "agent_step"
	AgentFinished          = "agent_finished"
	AgentFailed            = "agent_failed"
	TodoUpdated            = "todo.updated"
	RequestUserInput       = "request_user_input"
	GoalUpdated            = "goal.updated"
	GoalCleared            = "goal.cleared"
	ClarificationRequired  = "clarification_required"
	WorkspaceCreated       = "workspace.created"
	WorkspaceReady         = "workspace.ready"
	WorkspaceFailed        = "workspace.failed"
	WorkspaceStatusChanged = "workspace.status_changed"
	WorkspacePreserved     = "workspace.preserved"
	WorkspaceCleaned       = "workspace.cleaned"
	GitStatusChanged       = "git.status_changed"
	GitCommitCreated       = "git.commit_created"
)

type Event struct {
	Type      string         `json:"type"`
	RunID     int64          `json:"run_id"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
