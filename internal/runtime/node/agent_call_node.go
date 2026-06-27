package node

import (
	"context"
	"encoding/json"
	"fmt"

	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type AgentCallNode struct {
	Caller toolruntime.AgentCaller
}

type agentCallConfig struct {
	AgentID       int64          `json:"agent_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Input         map[string]any `json:"input"`
	MaxDepth      int            `json:"max_depth"`
}

func (AgentCallNode) Type() string { return "agent_call" }

func (AgentCallNode) Validate(config json.RawMessage) error {
	var cfg agentCallConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid agent_call config", agenterrors.ErrInvalidInput)
	}
	if cfg.AgentID <= 0 {
		return fmt.Errorf("%w: agent_call agent_id is required", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxDepth < 0 || cfg.MaxDepth > 5 {
		return fmt.Errorf("%w: agent_call max_depth must be <= 5", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n AgentCallNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Caller == nil {
		return nil, fmt.Errorf("agent_call dependency is not configured")
	}
	var cfg agentCallConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	inputMap := resolveAgentCallInput(cfg.Input, rc, input)
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.AgentCallStarted,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"agent_id":        cfg.AgentID,
			"flow_version_id": cfg.FlowVersionID,
			"call_depth":      rc.CallDepth,
		},
	})
	result, err := n.Caller.CallAgent(ctx, toolruntime.AgentCallRequest{
		OwnerID:       rc.OwnerID,
		ParentRunID:   rc.RunID,
		CallerAgentID: rc.AgentID,
		CallerNodeID:  rc.CurrentNodeID,
		AgentID:       cfg.AgentID,
		FlowVersionID: cfg.FlowVersionID,
		Input:         inputMap,
		CallDepth:     rc.CallDepth,
		MaxDepth:      cfg.MaxDepth,
	})
	if err != nil {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{
			Type:     runtimeevent.AgentCallFailed,
			RunID:    rc.RunID,
			NodeID:   rc.CurrentNodeID,
			NodeType: n.Type(),
			Payload:  map[string]any{"agent_id": cfg.AgentID, "error": err.Error()},
		})
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.AgentCallFinished,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"agent_id":   result.AgentID,
			"run_id":     result.RunID,
			"status":     result.Status,
			"latency_ms": result.LatencyMS,
		},
	})
	return engine.NodeOutput{
		"run_id":          result.RunID,
		"agent_id":        result.AgentID,
		"flow_version_id": result.FlowVersionID,
		"status":          result.Status,
		"output":          result.Output,
		"content":         result.Output["content"],
		"latency_ms":      result.LatencyMS,
	}, nil
}

func resolveAgentCallInput(configInput map[string]any, rc *engine.RunContext, input engine.NodeInput) map[string]any {
	if configInput != nil {
		resolved := engine.ResolveAny(configInput, rc)
		if value, ok := resolved.(map[string]any); ok && len(value) > 0 {
			return value
		}
	}
	if len(input) > 0 {
		return map[string]any(input)
	}
	if rc.Input != nil {
		return rc.Input
	}
	return map[string]any{}
}
