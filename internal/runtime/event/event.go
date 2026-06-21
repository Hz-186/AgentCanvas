package event

import "time"

const (
	WorkflowStarted     = "workflow_started"
	NodeStarted         = "node_started"
	NodeFinished        = "node_finished"
	NodeFailed          = "node_failed"
	RetrievalStarted    = "retrieval_started"
	RetrievalFinished   = "retrieval_finished"
	LLMStarted          = "llm_started"
	LLMDelta            = "llm_delta"
	LLMFinished         = "llm_finished"
	MessageCreated      = "message_created"
	MemoryReadStarted   = "memory_read_started"
	MemoryReadFinished  = "memory_read_finished"
	MemoryWriteStarted  = "memory_write_started"
	MemoryWriteFinished = "memory_write_finished"
	ToolStarted         = "tool_started"
	ToolFinished        = "tool_finished"
	ToolFailed          = "tool_failed"
	SwitchSelected      = "switch_selected"
	JSONOutputValidated = "json_output_validated"
	GuardrailPassed     = "guardrail_passed"
	GuardrailBlocked    = "guardrail_blocked"
	WorkflowFinished    = "workflow_finished"
	WorkflowFailed      = "workflow_failed"
)

type Event struct {
	Type      string         `json:"type"`
	RunID     int64          `json:"run_id"`
	NodeID    string         `json:"node_id,omitempty"`
	NodeType  string         `json:"node_type,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
