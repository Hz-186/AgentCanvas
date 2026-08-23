package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain"
)

const (
	RunTypeTurn     = "turn"
	RunTypeSubagent = "subagent"

	RunStatusRunning      = "running"
	RunStatusQueued       = "queued"
	RunStatusSucceeded    = "succeeded"
	RunStatusFailed       = "failed"
	RunStatusCancelled    = "cancelled"
	RunStatusWaitingHuman = "waiting_human"
	RunStatusPaused       = "paused"
	RunStatusResuming     = "resuming"
	RunStatusTimeout      = "timeout"
)

// Run is a durable execution of an Agent turn or temporary subagent.
type Run struct {
	domain.BaseModel
	AgentID         int64           `json:"agent_id" gorm:"column:agent_id"`
	AgentReleaseID  *int64          `json:"agent_release_id,omitempty" gorm:"column:agent_release_id"`
	ConversationID  *int64          `json:"conversation_id,omitempty" gorm:"column:conversation_id"`
	WorkspaceID     *int64          `json:"workspace_id,omitempty" gorm:"column:workspace_id"`
	ParentRunID     *int64          `json:"parent_run_id,omitempty" gorm:"column:parent_run_id"`
	RunType         string          `json:"run_type" gorm:"column:run_type"`
	DelegationDepth int             `json:"delegation_depth" gorm:"column:delegation_depth"`
	DefinitionJSON  json.RawMessage `json:"definition_json,omitempty" gorm:"column:definition_json"`
	DefinitionHash  string          `json:"definition_hash,omitempty" gorm:"column:definition_hash"`
	RuleHash        string          `json:"rule_hash,omitempty" gorm:"column:rule_hash"`
	Status          string          `json:"status" gorm:"column:status"`
	InputJSON       json.RawMessage `json:"input_json" gorm:"column:input_json"`
	OutputJSON      json.RawMessage `json:"output_json" gorm:"column:output_json"`
	ErrorMessage    string          `json:"error_message" gorm:"column:error_message"`
	TotalTokens     int             `json:"total_tokens" gorm:"column:total_tokens"`
	LatencyMS       int             `json:"latency_ms" gorm:"column:latency_ms"`
	StartedAt       time.Time       `json:"started_at" gorm:"column:started_at"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty" gorm:"column:finished_at"`
}

// TransitionStatus is the single state machine for durable Agent runs.
// Terminal states are immutable; resuming is only valid from a paused or
// human-waiting run.
func (r *Run) TransitionStatus(next string) error {
	if r == nil {
		return fmt.Errorf("run is nil")
	}
	if r.Status == next {
		return nil
	}
	if !canTransitionRunStatus(r.Status, next) {
		return fmt.Errorf("invalid run status transition %q -> %q", r.Status, next)
	}
	r.Status = next
	return nil
}

func canTransitionRunStatus(from, to string) bool {
	if from == "" {
		return to == RunStatusQueued || to == RunStatusRunning
	}
	switch from {
	case RunStatusQueued:
		return to == RunStatusRunning || to == RunStatusCancelled || to == RunStatusFailed
	case RunStatusRunning:
		// A running run can be returned to the queue by the lease recovery
		// worker when no tool side effect has been persisted yet.
		return to == RunStatusQueued || to == RunStatusWaitingHuman || to == RunStatusPaused || to == RunStatusSucceeded || to == RunStatusFailed || to == RunStatusCancelled || to == RunStatusTimeout
	case RunStatusWaitingHuman, RunStatusPaused:
		return to == RunStatusResuming || to == RunStatusCancelled || to == RunStatusFailed
	case RunStatusResuming:
		return to == RunStatusQueued || to == RunStatusRunning || to == RunStatusWaitingHuman || to == RunStatusPaused || to == RunStatusSucceeded || to == RunStatusFailed || to == RunStatusCancelled
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimeout:
		return false
	default:
		return false
	}
}

func IsActiveRunStatus(status string) bool {
	switch status {
	case RunStatusQueued, RunStatusRunning, RunStatusResuming, RunStatusWaitingHuman, RunStatusPaused:
		return true
	default:
		return false
	}
}

func (Run) TableName() string { return "agent_runs" }

type RunEvent struct {
	domain.ImmutableModel
	RunID       int64           `json:"run_id" gorm:"column:run_id"`
	EventType   string          `json:"event_type" gorm:"column:event_type"`
	PayloadJSON json.RawMessage `json:"payload_json" gorm:"column:payload_json"`
}

func (RunEvent) TableName() string { return "agent_run_events" }

type RunStep struct {
	domain.ImmutableModel
	RunID         int64           `json:"run_id" gorm:"column:run_id"`
	StepIndex     int             `json:"step_index" gorm:"column:step_index"`
	StepType      string          `json:"step_type" gorm:"column:step_type"`
	Role          string          `json:"role" gorm:"column:role"`
	Content       string          `json:"content" gorm:"column:content"`
	ToolCallID    string          `json:"tool_call_id" gorm:"column:tool_call_id"`
	ToolName      string          `json:"tool_name" gorm:"column:tool_name"`
	ArgumentsJSON json.RawMessage `json:"arguments_json" gorm:"column:arguments_json"`
	OutputJSON    json.RawMessage `json:"output_json" gorm:"column:output_json"`
	Compressed    bool            `json:"compressed" gorm:"column:compressed"`
	ErrorMessage  string          `json:"error_message" gorm:"column:error_message"`
	TokenCount    int             `json:"token_count" gorm:"column:token_count"`
	LatencyMS     int             `json:"latency_ms" gorm:"column:latency_ms"`
	ProviderID    int64           `json:"provider_id" gorm:"column:provider_id"`
	Model         string          `json:"model" gorm:"column:model"`
}

func (RunStep) TableName() string { return "agent_run_steps" }
