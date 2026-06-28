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

type WorkflowCallNode struct {
	Caller toolruntime.WorkflowCaller
}

type workflowCallConfig struct {
	WorkflowID    int64          `json:"workflow_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Input         map[string]any `json:"input"`
	MaxDepth      int            `json:"max_depth"`
}

func (WorkflowCallNode) Type() string { return "workflow_call" }

func (WorkflowCallNode) Validate(config json.RawMessage) error {
	var cfg workflowCallConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid workflow_call config", agenterrors.ErrInvalidInput)
	}
	if cfg.WorkflowID <= 0 {
		return fmt.Errorf("%w: workflow_call workflow_id is required", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxDepth < 0 || cfg.MaxDepth > 5 {
		return fmt.Errorf("%w: workflow_call max_depth must be <= 5", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n WorkflowCallNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Caller == nil {
		return nil, fmt.Errorf("workflow_call dependency is not configured")
	}
	var cfg workflowCallConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	inputMap := resolveWorkflowCallInput(cfg.Input, rc, input)
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.WorkflowCallStarted,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"workflow_id":     cfg.WorkflowID,
			"flow_version_id": cfg.FlowVersionID,
			"call_depth":      rc.CallDepth,
		},
	})
	result, err := n.Caller.CallWorkflow(ctx, toolruntime.WorkflowCallRequest{
		OwnerID:           rc.OwnerID,
		ParentRunID:       rc.RunID,
		CallerWorkflowID:  rc.WorkflowID,
		CallerNodeID:      rc.CurrentNodeID,
		WorkflowID:        cfg.WorkflowID,
		FlowVersionID:     cfg.FlowVersionID,
		Input:             inputMap,
		CallDepth:         rc.CallDepth,
		WorkflowCallChain: append([]int64(nil), rc.WorkflowCallChain...),
		MaxDepth:          cfg.MaxDepth,
	})
	if err != nil {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{
			Type:     runtimeevent.WorkflowCallFailed,
			RunID:    rc.RunID,
			NodeID:   rc.CurrentNodeID,
			NodeType: n.Type(),
			Payload:  map[string]any{"workflow_id": cfg.WorkflowID, "error": err.Error()},
		})
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.WorkflowCallFinished,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"workflow_id": result.WorkflowID,
			"run_id":      result.RunID,
			"status":      result.Status,
			"latency_ms":  result.LatencyMS,
		},
	})
	return engine.NodeOutput{
		"run_id":          result.RunID,
		"workflow_id":     result.WorkflowID,
		"flow_version_id": result.FlowVersionID,
		"status":          result.Status,
		"output":          result.Output,
		"content":         result.Output["content"],
		"latency_ms":      result.LatencyMS,
	}, nil
}

func resolveWorkflowCallInput(configInput map[string]any, rc *engine.RunContext, input engine.NodeInput) map[string]any {
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
