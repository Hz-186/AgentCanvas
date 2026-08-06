package event

import (
	"encoding/json"
	"time"
)

const RunStreamVersion = 1

const (
	AssistantStart   = "assistant.start"
	AssistantDelta   = "assistant.delta"
	AssistantEnd     = "assistant.end"
	ReasoningStart   = "reasoning.start"
	ReasoningDelta   = "reasoning.delta"
	ReasoningEnd     = "reasoning.end"
	StatusUpdate     = "status.update"
	ToolStart        = "tool.start"
	ToolProgress     = "tool.progress"
	ToolComplete     = "tool.complete"
	ToolError        = "tool.error"
	ApprovalRequired = "approval.required"
	UsageUpdate      = "usage.update"
	StreamSnapshot   = "stream.snapshot"
	RunComplete      = "run.complete"
	RunFailed        = "run.failed"
	RunPaused        = "run.paused"
	RunWaiting       = "run.waiting"
	RunCancelled     = "run.cancelled"
)

// RunStreamEvent is the only public v1 envelope. Type and Payload are kept as
// non-JSON migration sidecars for the legacy runtime Event adapter.
type RunStreamEvent struct {
	Version        int             `json:"version"`
	RunID          int64           `json:"run_id"`
	ConversationID *int64          `json:"conversation_id,omitempty"`
	Seq            uint64          `json:"seq"`
	Kind           string          `json:"kind"`
	CreatedAt      time.Time       `json:"created_at"`
	Data           json.RawMessage `json:"data,omitempty"`

	Type    string         `json:"-"`
	Payload map[string]any `json:"-"`
}

type TextPayload struct {
	SegmentID string `json:"segment_id"`
	Text      string `json:"text,omitempty"`
}

type StatusPayload struct {
	Message  string `json:"message"`
	Level    string `json:"level"`
	Degraded bool   `json:"degraded,omitempty"`
}

type ToolPayload struct {
	CallID    string          `json:"call_id"`
	SegmentID string          `json:"segment_id"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	Output    json.RawMessage `json:"output,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

type ApprovalOptionPayload struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type ApprovalPayload struct {
	RequestID int64                   `json:"request_id"`
	CallID    string                  `json:"call_id"`
	ToolName  string                  `json:"tool_name"`
	Reason    string                  `json:"reason"`
	Options   []ApprovalOptionPayload `json:"options,omitempty"`
}

type UsagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type TerminalSnapshotPayload struct {
	Run     json.RawMessage `json:"run"`
	Turn    json.RawMessage `json:"turn,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
	Usage   UsagePayload    `json:"usage"`
}
