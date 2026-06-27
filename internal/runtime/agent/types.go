package agent

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

const (
	StopReasonFinalAnswer      = "final_answer"
	StopReasonMaxIterations    = "max_iterations_exceeded"
	StopReasonMaxToolCalls     = "max_tool_calls_exceeded"
	StopReasonTimeout          = "timeout"
	StopReasonCancelled        = "cancelled"
	StopReasonLLMError         = "llm_error"
	StopReasonToolNameNotFound = "tool_name_not_found"
)

const (
	StepTypeLLMResponse = "llm_response"
	StepTypeToolCall    = "tool_call"
	StepTypeToolResult  = "tool_result"
	StepTypeFinalAnswer = "final_answer"
	StepTypeError       = "error"
)

type RunRequest struct {
	OwnerID            int64
	AgentID            int64
	RunID              int64
	NodeID             string
	CallDepth          int
	Provider           llm.ChatProviderConfig
	Model              string
	SystemPrompt       string
	Task               string
	Temperature        *float64
	MaxIterations      int
	MaxToolCalls       int
	MaxExecutionTimeMS int
	Tools              []toolruntime.RuntimeTool
}

type RunResult struct {
	FinalAnswer string    `json:"final_answer"`
	StopReason  string    `json:"stop_reason"`
	Iterations  int       `json:"iterations"`
	ToolCalls   int       `json:"tool_calls"`
	Usage       llm.Usage `json:"usage"`
	Steps       []RunStep `json:"steps,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	LatencyMS   int       `json:"latency_ms"`
}

type RunStep struct {
	Index         int             `json:"index"`
	Type          string          `json:"type"`
	Role          string          `json:"role,omitempty"`
	Content       string          `json:"content,omitempty"`
	ToolCallID    string          `json:"tool_call_id,omitempty"`
	ToolName      string          `json:"tool_name,omitempty"`
	ArgumentsJSON json.RawMessage `json:"arguments_json,omitempty"`
	OutputJSON    json.RawMessage `json:"output_json,omitempty"`
	IsError       bool            `json:"is_error,omitempty"`
	Error         string          `json:"error,omitempty"`
	LatencyMS     int             `json:"latency_ms,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}
