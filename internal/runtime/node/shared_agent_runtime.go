package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

// AgentRuntime is the workflow-independent execution contract shared by
// Agent Chat and the Workflow agent_loop adapter.
type AgentRuntime interface {
	Execute(ctx context.Context, req AgentRunRequest, emit engine.EventEmitter) (*AgentRunResult, error)
	Resume(ctx context.Context, req AgentResumeRequest, emit engine.EventEmitter) (*AgentRunResult, error)
}

// AgentRuntimeDefinition is the normalized runtime form of an immutable Agent
// release. It intentionally mirrors agent_loop configuration so both callers
// execute the same loader, policy, approval and reflection code.
type AgentRuntimeDefinition agentRuntimeConfig

type AgentRunRequest struct {
	OwnerID        int64
	AgentID        int64
	AgentReleaseID int64
	RunID          int64
	ConversationID *int64
	Task           string
	Definition     AgentRuntimeDefinition
	StepRecorder   engine.AgentStepRecorder
	ContextBlocks  []runtimeagent.ContextBlock
}

type AgentResumeRequest struct {
	AgentRunRequest
	Checkpoint    *runtimeagent.Checkpoint
	Approved      bool
	RejectionNote string
}

type AgentRunResult struct {
	Output engine.NodeOutput `json:"output"`
}

type SharedAgentRuntime struct{ node AgentNode }

func (r *SharedAgentRuntime) ConfigureAgentCaller(caller toolruntime.AgentCaller) {
	r.node.AgentCaller = caller
}

func (r *SharedAgentRuntime) ConfigureSessionSearch(index conversation.MessageSearchIndex) {
	r.node.SessionSearch = index
}

func (r *SharedAgentRuntime) ConfigureMemoryReader(reader MemoryBatchReader) {
	r.node.MemoryReader = reader
}

func (r *SharedAgentRuntime) ConfigureMemoryCandidates(candidates memory.CandidateWriter) {
	r.node.MemoryCandidates = candidates
}

func NewSharedAgentRuntime(deps Deps) (*SharedAgentRuntime, error) {
	if deps.ToolCalling == nil {
		return nil, fmt.Errorf("tool calling client is required")
	}
	if deps.Sandbox == nil {
		return nil, fmt.Errorf("sandbox runner is required")
	}
	return &SharedAgentRuntime{node: buildAgentNode(deps)}, nil
}

func DecodeAgentRuntimeDefinition(raw json.RawMessage) (AgentRuntimeDefinition, error) {
	var cfg agentRuntimeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return AgentRuntimeDefinition{}, err
	}
	var release struct {
		Role      string          `json:"role"`
		Goal      string          `json:"goal"`
		Backstory string          `json:"backstory"`
		Rules     json.RawMessage `json:"rules_json"`
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return AgentRuntimeDefinition{}, err
	}
	identity := make([]string, 0, 3)
	if value := strings.TrimSpace(release.Role); value != "" {
		identity = append(identity, "ROLE: "+value)
	}
	if value := strings.TrimSpace(release.Goal); value != "" {
		identity = append(identity, "GOAL: "+value)
	}
	if value := strings.TrimSpace(release.Backstory); value != "" {
		identity = append(identity, "BACKGROUND: "+value)
	}
	if len(identity) > 0 {
		cfg.SystemPrompt = strings.Join(identity, "\n") + "\n\n" + cfg.SystemPrompt
	}
	if len(release.Rules) > 0 && string(release.Rules) != "null" {
		if err := json.Unmarshal(release.Rules, &cfg.Rules); err != nil {
			return AgentRuntimeDefinition{}, fmt.Errorf("decode agent release rules: %w", err)
		}
		if _, err := rules.ValidateRules(cfg.Rules); err != nil {
			return AgentRuntimeDefinition{}, fmt.Errorf("validate agent release rules: %w", err)
		}
	}
	return AgentRuntimeDefinition(normalizeLegacyAgentMode(cfg)), nil
}

func (r *SharedAgentRuntime) Execute(ctx context.Context, req AgentRunRequest, emit engine.EventEmitter) (*AgentRunResult, error) {
	return r.run(ctx, req, emit, nil)
}

func (r *SharedAgentRuntime) Resume(ctx context.Context, req AgentResumeRequest, emit engine.EventEmitter) (*AgentRunResult, error) {
	return r.run(ctx, req.AgentRunRequest, emit, &AgentResumeOptions{
		Checkpoint: req.Checkpoint, Approved: req.Approved, RejectionNote: req.RejectionNote,
	})
}

func (r *SharedAgentRuntime) run(ctx context.Context, req AgentRunRequest, emit engine.EventEmitter, resume *AgentResumeOptions) (*AgentRunResult, error) {
	conversationID := req.ConversationID
	rc := &engine.RunContext{
		OwnerID: req.OwnerID, AgentID: req.AgentID, AgentReleaseID: req.AgentReleaseID,
		RunID: req.RunID, ConversationID: conversationID, Input: map[string]any{"query": req.Task},
		Variables: map[string]any{}, NodeInputs: map[string]engine.NodeInput{}, NodeOutputs: map[string]engine.NodeOutput{},
		NodeErrors: map[string]string{}, NodeLatencies: map[string]int{}, ExecutedNodes: map[string]bool{},
		Events: emit, AgentSteps: req.StepRecorder, CurrentNodeID: "agent", CurrentNodeType: "agent_runtime",
	}
	cfg := agentRuntimeConfig(req.Definition)
	cfg.TaskTemplate = req.Task
	// Independent Agent Chat never executes historical static delegation
	// allowlists. New child work is always model-authored through run_subagent;
	// old fields remain decode-only so historical releases can still be read.
	cfg.CallWorkflowIDs = nil
	cfg.CallAgentIDs = nil
	cfg.AllowInlineAgents = true
	cfg.CallWorkflowToolName = ""
	cfg.AdditionalContextBlocks = append([]runtimeagent.ContextBlock(nil), req.ContextBlocks...)
	output, err := r.node.runAgent(ctx, rc, engine.NodeInput{"query": req.Task}, cfg, "agent_runtime", false, resume)
	if err != nil {
		return nil, err
	}
	return &AgentRunResult{Output: output}, nil
}
