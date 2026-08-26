package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type SubagentDefinition struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description,omitempty"`
	SystemPrompt           string   `json:"system_prompt"`
	Task                   string   `json:"task"`
	Mode                   string   `json:"mode,omitempty"`
	ProviderID             int64    `json:"provider_id"`
	Model                  string   `json:"model,omitempty"`
	ToolIDs                []int64  `json:"tool_ids,omitempty"`
	SkillIDs               []int64  `json:"skill_ids,omitempty"`
	KnowledgeBaseIDs       []int64  `json:"knowledge_base_ids,omitempty"`
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
	WorkspaceMode          string   `json:"workspace_mode,omitempty"`
}

type SubagentRequest struct {
	OwnerID         int64              `json:"owner_id"`
	ParentRunID     int64              `json:"parent_run_id"`
	AgentID         int64              `json:"agent_id"`
	ConversationID  *int64             `json:"conversation_id,omitempty"`
	ProjectID       int64              `json:"project_id,omitempty"`
	DelegationDepth int                `json:"delegation_depth"`
	MaxDepth        int                `json:"max_depth"`
	Definition      SubagentDefinition `json:"definition"`
	Workspace       *WorkspaceContext  `json:"workspace,omitempty"`
}

type SubagentResult struct {
	RunID     int64          `json:"run_id"`
	Status    string         `json:"status"`
	Output    map[string]any `json:"output"`
	Error     string         `json:"error,omitempty"`
	LatencyMS int            `json:"latency_ms"`
}

type SubagentDispatcher interface {
	RunSubagent(ctx context.Context, req SubagentRequest) (*SubagentResult, error)
}

type DefaultSubagentConfig struct {
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

type SubagentTool struct {
	Dispatcher SubagentDispatcher
	Default    DefaultSubagentConfig
}

func (SubagentTool) Name() string { return "run_subagent" }

func (SubagentTool) Description() string {
	return "Define and run an independent temporary sub-agent for a focused task. Use several calls in one response to run specialists concurrently."
}

func (SubagentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Optional label chosen by the parent model."},"description":{"type":"string"},"system_prompt":{"type":"string","description":"Optional role instructions chosen for this task."},"task":{"type":"string"},"mode":{"type":"string","enum":["default","plan"]},"workspace_mode":{"type":"string","enum":["inherit","shared","worktree"]},"model":{"type":"string"},"tool_ids":{"type":"array","items":{"type":"number"}},"skill_ids":{"type":"array","items":{"type":"number"}},"knowledge_base_ids":{"type":"array","items":{"type":"number"}},"mcp_server_ids":{"type":"array","items":{"type":"number"}},"max_iterations":{"type":"number"},"max_tool_calls":{"type":"number"},"max_execution_time_ms":{"type":"number"}},"required":["task"],"additionalProperties":false}`)
}

func (SubagentTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectExternalAction, ExecutionClass: ExecutionDelegation}
}

func (t SubagentTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Dispatcher == nil {
		return nil, fmt.Errorf("subagent dispatcher is not configured")
	}
	var definition SubagentDefinition
	if err := json.Unmarshal(input, &definition); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	definition.Name = strings.TrimSpace(definition.Name)
	definition.SystemPrompt = strings.TrimSpace(definition.SystemPrompt)
	definition.Task = strings.TrimSpace(definition.Task)
	if definition.Task == "" {
		return &ToolResult{ContentText: "task is required", IsError: true}, fmt.Errorf("task is required")
	}
	if definition.Mode != "" && definition.Mode != "plan" && definition.Mode != "default" {
		return &ToolResult{ContentText: "mode must be default or plan", IsError: true}, fmt.Errorf("mode must be default or plan")
	}
	if definition.Name == "" {
		definition.Name = "subagent"
	}
	if definition.SystemPrompt == "" {
		definition.SystemPrompt = "You are a focused sub-agent. Infer the role required by the task, work independently, and return concise evidence for the parent agent. Do not claim work you did not perform."
	}
	if len(definition.Name) > 128 || len(definition.SystemPrompt) > 16000 || len(definition.Task) > 16000 {
		return &ToolResult{ContentText: "subagent definition is too large", IsError: true}, fmt.Errorf("subagent definition is too large")
	}
	if !isSubset(definition.ToolIDs, t.Default.AllowedToolIDs) || !isSubset(definition.SkillIDs, t.Default.AllowedSkillIDs) || !isSubset(definition.KnowledgeBaseIDs, t.Default.AllowedKnowledgeIDs) || !isSubset(definition.MCPServerIDs, t.Default.AllowedMCPServerIDs) {
		return &ToolResult{ContentText: "subagent requested resources outside parent policy", IsError: true}, fmt.Errorf("subagent requested resources outside parent policy")
	}
	if len(definition.ToolIDs) == 0 {
		definition.ToolIDs = append([]int64(nil), t.Default.AllowedToolIDs...)
	}
	if len(definition.SkillIDs) == 0 {
		definition.SkillIDs = append([]int64(nil), t.Default.AllowedSkillIDs...)
	}
	if len(definition.KnowledgeBaseIDs) == 0 {
		definition.KnowledgeBaseIDs = append([]int64(nil), t.Default.AllowedKnowledgeIDs...)
	}
	if len(definition.MCPServerIDs) == 0 {
		definition.MCPServerIDs = append([]int64(nil), t.Default.AllowedMCPServerIDs...)
	}
	definition.ProviderID = t.Default.ProviderID
	if requestedModel := strings.TrimSpace(definition.Model); requestedModel != "" && requestedModel != t.Default.Model {
		return &ToolResult{ContentText: "subagent model is outside parent policy", IsError: true}, fmt.Errorf("subagent model is outside parent policy")
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
	if definition.WorkspaceMode != "" && definition.WorkspaceMode != "inherit" && definition.WorkspaceMode != "shared" && definition.WorkspaceMode != "worktree" {
		return &ToolResult{ContentText: "workspace_mode must be inherit, shared, or worktree", IsError: true}, fmt.Errorf("workspace_mode must be inherit, shared, or worktree")
	}
	result, err := t.Dispatcher.RunSubagent(ctx, SubagentRequest{OwnerID: rc.OwnerID, ParentRunID: rc.RunID, AgentID: rc.AgentID, ConversationID: rc.ConversationID, ProjectID: projectIDFromToolRunContext(rc), DelegationDepth: rc.DelegationDepth, MaxDepth: definition.MaxDepth, Definition: definition, Workspace: rc.Workspace})
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
