package event

import "time"

const (
	NodeStarted            = "node_started"
	NodeFinished           = "node_finished"
	NodeFailed             = "node_failed"
	RetrievalStarted       = "retrieval_started"
	RetrievalFinished      = "retrieval_finished"
	LLMStarted             = "llm_started"
	LLMDelta               = "llm_delta"
	LLMFinished            = "llm_finished"
	MessageCreated         = "message_created"
	MemoryReadStarted      = "memory_read_started"
	MemoryReadFinished     = "memory_read_finished"
	MemoryWriteStarted     = "memory_write_started"
	MemoryWriteFinished    = "memory_write_finished"
	ToolStarted            = "tool_started"
	ToolFinished           = "tool_finished"
	ToolFailed             = "tool_failed"
	AgentStarted           = "agent_started"
	AgentStep              = "agent_step"
	AgentFinished          = "agent_finished"
	AgentFailed            = "agent_failed"
	TodoUpdated            = "todo.updated"
	RequestUserInput       = "request_user_input"
	GoalUpdated            = "goal.updated"
	GoalCleared            = "goal.cleared"
	ClarificationRequired  = "clarification_required"
	SandboxStarted         = "sandbox_started"
	SandboxFinished        = "sandbox_finished"
	SandboxFailed          = "sandbox_failed"
	SwitchSelected         = "switch_selected"
	JSONOutputValidated    = "json_output_validated"
	GuardrailPassed        = "guardrail_passed"
	GuardrailBlocked       = "guardrail_blocked"
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
