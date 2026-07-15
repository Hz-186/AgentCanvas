package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type InlineAgentDefinition struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description,omitempty"`
	SystemPrompt           string   `json:"system_prompt"`
	Task                   string   `json:"task"`
	Mode                   string   `json:"mode,omitempty"`
	ProviderID             int64    `json:"provider_id"`
	Model                  string   `json:"model,omitempty"`
	ToolIDs                []int64  `json:"tool_ids,omitempty"`
	SkillIDs               []int64  `json:"skill_ids,omitempty"`
	KnowledgeIDs           []int64  `json:"knowledge_ids,omitempty"`
	MCPServerIDs           []int64  `json:"mcp_server_ids,omitempty"`
	MaxIterations          int      `json:"max_iterations,omitempty"`
	MaxToolCalls           int      `json:"max_tool_calls,omitempty"`
	MaxExecutionTimeMS     int      `json:"max_execution_time_ms,omitempty"`
	MaxParallelChildren    int      `json:"max_parallel_children,omitempty"`
	MaxDepth               int      `json:"max_depth,omitempty"`
	RequireApprovalForRisk []string `json:"require_approval_for_risk,omitempty"`
	MaxToolTimeoutMS       int      `json:"max_tool_timeout_ms,omitempty"`
	MaxToolOutputBytes     int      `json:"max_tool_output_bytes,omitempty"`
	AllowedHosts           []string `json:"allowed_hosts,omitempty"`
	CodeExecutionEnabled   bool     `json:"code_execution_enabled,omitempty"`
}

type InlineAgentCallRequest struct {
	OwnerID           int64                 `json:"owner_id"`
	ParentRunID       int64                 `json:"parent_run_id"`
	CallerWorkflowID  int64                 `json:"caller_workflow_id"`
	CallerAgentID     int64                 `json:"caller_agent_id"`
	CallerNodeID      string                `json:"caller_node_id"`
	FlowVersionID     int64                 `json:"flow_version_id"`
	ConversationID    *int64                `json:"conversation_id,omitempty"`
	CallDepth         int                   `json:"call_depth"`
	WorkflowCallChain []int64               `json:"workflow_call_chain"`
	MaxDepth          int                   `json:"max_depth"`
	Definition        InlineAgentDefinition `json:"definition"`
}

type InlineAgentCallResult struct {
	RunID     int64          `json:"run_id"`
	Status    string         `json:"status"`
	Output    map[string]any `json:"output"`
	Error     string         `json:"error,omitempty"`
	LatencyMS int            `json:"latency_ms"`
}

type InlineAgentCaller interface {
	CallInlineAgent(ctx context.Context, req InlineAgentCallRequest) (*InlineAgentCallResult, error)
}

type DefaultAgentConfig struct {
	ProviderID             int64
	Model                  string
	AllowedToolIDs         []int64
	AllowedSkillIDs        []int64
	AllowedKnowledgeIDs    []int64
	AllowedMCPServerIDs    []int64
	MaxIterations          int
	MaxToolCalls           int
	MaxExecutionTimeMS     int
	MaxParallelChildren    int
	MaxDepth               int
	RequireApprovalForRisk []string
	MaxToolTimeoutMS       int
	MaxToolOutputBytes     int
	AllowedHosts           []string
	CodeExecutionEnabled   bool
}

type InlineAgentTool struct {
	Caller  InlineAgentCaller
	Default DefaultAgentConfig
}

func (InlineAgentTool) Name() string { return "run_subagent" }

func (InlineAgentTool) Description() string {
	return "Define and run an independent temporary sub-agent for a focused task. Use several calls in one response to run specialists concurrently."
}

func (InlineAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"},"system_prompt":{"type":"string"},"task":{"type":"string"},"mode":{"type":"string","enum":["react","plan_execute","reflect"]},"model":{"type":"string"},"tool_ids":{"type":"array","items":{"type":"number"}},"skill_ids":{"type":"array","items":{"type":"number"}},"knowledge_ids":{"type":"array","items":{"type":"number"}},"mcp_server_ids":{"type":"array","items":{"type":"number"}},"max_iterations":{"type":"number"},"max_tool_calls":{"type":"number"},"max_execution_time_ms":{"type":"number"}},"required":["name","system_prompt","task"],"additionalProperties":false}`)
}

func (InlineAgentTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectExternalAction, ExecutionClass: ExecutionDelegation}
}

func (t InlineAgentTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Caller == nil {
		return nil, fmt.Errorf("inline agent caller is not configured")
	}
	var definition InlineAgentDefinition
	if err := json.Unmarshal(input, &definition); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	definition.Name = strings.TrimSpace(definition.Name)
	definition.SystemPrompt = strings.TrimSpace(definition.SystemPrompt)
	definition.Task = strings.TrimSpace(definition.Task)
	if definition.Name == "" || definition.SystemPrompt == "" || definition.Task == "" {
		return &ToolResult{ContentText: "name, system_prompt, and task are required", IsError: true}, fmt.Errorf("name, system_prompt, and task are required")
	}
	if len(definition.Name) > 128 || len(definition.SystemPrompt) > 16000 || len(definition.Task) > 16000 {
		return &ToolResult{ContentText: "inline agent definition is too large", IsError: true}, fmt.Errorf("inline agent definition is too large")
	}
	if !isSubset(definition.ToolIDs, t.Default.AllowedToolIDs) || !isSubset(definition.SkillIDs, t.Default.AllowedSkillIDs) || !isSubset(definition.KnowledgeIDs, t.Default.AllowedKnowledgeIDs) || !isSubset(definition.MCPServerIDs, t.Default.AllowedMCPServerIDs) {
		return &ToolResult{ContentText: "inline agent requested resources outside default_agent policy", IsError: true}, fmt.Errorf("inline agent requested resources outside default_agent policy")
	}
	definition.ProviderID = t.Default.ProviderID
	if requestedModel := strings.TrimSpace(definition.Model); requestedModel != "" && requestedModel != t.Default.Model {
		return &ToolResult{ContentText: "inline agent model is outside default_agent policy", IsError: true}, fmt.Errorf("inline agent model is outside default_agent policy")
	}
	definition.Model = t.Default.Model
	definition.CodeExecutionEnabled = t.Default.CodeExecutionEnabled
	definition.MaxIterations = boundedDefault(definition.MaxIterations, t.Default.MaxIterations, 50)
	definition.MaxToolCalls = boundedDefault(definition.MaxToolCalls, t.Default.MaxToolCalls, 100)
	definition.MaxExecutionTimeMS = boundedDefault(definition.MaxExecutionTimeMS, t.Default.MaxExecutionTimeMS, 600000)
	definition.MaxParallelChildren = boundedDefault(0, t.Default.MaxParallelChildren, 64)
	definition.MaxDepth = boundedDefault(0, t.Default.MaxDepth, 5)
	definition.RequireApprovalForRisk = append([]string(nil), t.Default.RequireApprovalForRisk...)
	definition.MaxToolTimeoutMS = t.Default.MaxToolTimeoutMS
	definition.MaxToolOutputBytes = t.Default.MaxToolOutputBytes
	definition.AllowedHosts = append([]string(nil), t.Default.AllowedHosts...)
	result, err := t.Caller.CallInlineAgent(ctx, InlineAgentCallRequest{OwnerID: rc.OwnerID, ParentRunID: rc.RunID, CallerWorkflowID: rc.WorkflowID, CallerAgentID: rc.AgentID, CallerNodeID: rc.NodeID, ConversationID: rc.ConversationID, CallDepth: rc.CallDepth, WorkflowCallChain: append([]int64(nil), rc.WorkflowCallChain...), MaxDepth: definition.MaxDepth, Definition: definition})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(result)
}

func isSubset(values, allowed []int64) bool {
	for _, value := range values {
		if value <= 0 || !slices.Contains(allowed, value) {
			return false
		}
	}
	return true
}

func boundedDefault(value, fallback, maximum int) int {
	if fallback <= 0 {
		switch maximum {
		case 50:
			fallback = 8
		case 100:
			fallback = 16
		case 600000:
			fallback = 120000
		case 64:
			fallback = 8
		case 5:
			fallback = 2
		default:
			fallback = maximum
		}
	}
	if fallback > maximum {
		fallback = maximum
	}
	if value <= 0 {
		return fallback
	}
	if value > fallback {
		return fallback
	}
	return value
}
