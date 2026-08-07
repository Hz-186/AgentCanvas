package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

// Runtime is the only execution contract for Agent turns and subagents.
type Runtime interface {
	Execute(ctx context.Context, req RunRequest, emit EventEmitter) (*RunResult, error)
	Resume(ctx context.Context, req ResumeRequest, emit EventEmitter) (*RunResult, error)
}

// Definition is the normalized runtime form of an immutable Agent release.
type Definition agentRuntimeConfig

type RunRequest struct {
	OwnerID         int64
	AgentID         int64
	AgentReleaseID  int64
	RunID           int64
	ParentRunID     *int64
	DelegationDepth int
	RuleHash        string
	ConversationID  *int64
	Task            string
	Definition      Definition
	StepRecorder    AgentStepRecorder
	ContextBlocks   []runtimeagent.ContextBlock
	Workspace       *toolruntime.WorkspaceContext
}

type ResumeRequest struct {
	RunRequest
	Checkpoint    *runtimeagent.Checkpoint
	Approved      bool
	RejectionNote string
}

type RunResult struct {
	Output RunOutput `json:"output"`
}

type AgentRuntime struct{ core runtimeCore }

func (r *AgentRuntime) ConfigureSubagentDispatcher(dispatcher toolruntime.SubagentDispatcher) {
	r.core.SubagentDispatcher = dispatcher
}

func (r *AgentRuntime) ConfigureSessionSearch(index conversation.MessageSearchIndex) {
	r.core.SessionSearch = index
}

func (r *AgentRuntime) ConfigureMemoryReader(reader MemoryBatchReader) {
	r.core.MemoryReader = reader
}

func (r *AgentRuntime) ConfigureMemoryCandidates(candidates memory.CandidateWriter) {
	r.core.MemoryCandidates = candidates
}

func New(deps Deps) (*AgentRuntime, error) {
	if deps.ToolCalling == nil {
		return nil, fmt.Errorf("tool calling client is required")
	}
	if deps.Sandbox == nil {
		return nil, fmt.Errorf("sandbox runner is required")
	}
	return &AgentRuntime{core: buildRuntimeCore(deps)}, nil
}

func DecodeDefinition(raw json.RawMessage) (Definition, error) {
	var cfg agentRuntimeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Definition{}, err
	}
	var release struct {
		Role      string          `json:"role"`
		Goal      string          `json:"goal"`
		Backstory string          `json:"backstory"`
		Rules     json.RawMessage `json:"rules_json"`
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return Definition{}, err
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
			return Definition{}, fmt.Errorf("decode agent release rules: %w", err)
		}
		if _, err := rules.ValidateRules(cfg.Rules); err != nil {
			return Definition{}, fmt.Errorf("validate agent release rules: %w", err)
		}
	}
	return Definition(cfg), nil
}

func (r *AgentRuntime) Execute(ctx context.Context, req RunRequest, emit EventEmitter) (*RunResult, error) {
	return r.run(ctx, req, emit, nil)
}

func (r *AgentRuntime) Resume(ctx context.Context, req ResumeRequest, emit EventEmitter) (*RunResult, error) {
	return r.run(ctx, req.RunRequest, emit, &AgentResumeOptions{
		Checkpoint: req.Checkpoint, Approved: req.Approved, RejectionNote: req.RejectionNote,
	})
}

func (r *AgentRuntime) run(ctx context.Context, req RunRequest, emit EventEmitter, resume *AgentResumeOptions) (*RunResult, error) {
	conversationID := req.ConversationID
	rc := &RunContext{
		OwnerID: req.OwnerID, AgentID: req.AgentID, AgentReleaseID: req.AgentReleaseID,
		RuleHash: req.RuleHash, RunID: req.RunID, ParentRunID: req.ParentRunID, DelegationDepth: req.DelegationDepth,
		ConversationID: conversationID, Input: map[string]any{"query": req.Task},
		Workspace: req.Workspace,
		Events:    emit, AgentSteps: req.StepRecorder,
	}
	cfg := agentRuntimeConfig(req.Definition)
	cfg.RuleHash = req.RuleHash
	cfg.TaskTemplate = req.Task
	// Child work is model-authored through run_subagent.
	cfg.AllowSubagents = true
	cfg.AdditionalContextBlocks = append([]runtimeagent.ContextBlock(nil), req.ContextBlocks...)
	output, err := r.core.runAgent(ctx, rc, RunInput{"query": req.Task}, cfg, resume)
	if err != nil {
		return nil, err
	}
	return &RunResult{Output: output}, nil
}

var _ Runtime = (*AgentRuntime)(nil)
