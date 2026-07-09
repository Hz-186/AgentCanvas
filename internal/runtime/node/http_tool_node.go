package node

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type HTTPToolNode struct {
	Tools       tool.DefinitionRepository
	Invocations tool.InvocationRepository
}

type httpToolConfig struct {
	ToolID int64          `json:"tool_id"`
	Input  map[string]any `json:"input"`
}

func (HTTPToolNode) Type() string { return "http_tool" }

func (HTTPToolNode) Validate(config json.RawMessage) error {
	var cfg httpToolConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid http_tool config", agenterrors.ErrInvalidInput)
	}
	if cfg.ToolID <= 0 {
		return fmt.Errorf("%w: http_tool tool_id is required", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n HTTPToolNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Tools == nil {
		return nil, fmt.Errorf("tool repository is not configured")
	}
	var cfg httpToolConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	def, err := n.Tools.FindByID(ctx, rc.OwnerID, cfg.ToolID)
	if err != nil {
		return nil, err
	}
	if def.Status != tool.StatusActive || def.ToolType != tool.TypeHTTP {
		return nil, fmt.Errorf("%w: http tool is not active", agenterrors.ErrInvalidInput)
	}
	resolvedInput := engine.ResolveAny(cfg.Input, rc)
	inputJSON, _ := json.Marshal(resolvedInput)
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ToolStarted, RunID: rc.RunID, Payload: map[string]any{"tool_id": def.ID, "tool_name": def.Name}})
	started := time.Now()
	output, callErr := ExecuteHTTPToolDefinition(ctx, def, inputJSON)
	status := tool.InvocationStatusSucceeded
	errMessage := ""
	if callErr != nil {
		status = tool.InvocationStatusFailed
		errMessage = callErr.Error()
	}
	outputJSON, _ := json.Marshal(output)
	latencyMS := int(time.Since(started).Milliseconds())
	if n.Invocations != nil {
		_ = n.Invocations.Create(ctx, &tool.Invocation{
			OwnerID: rc.OwnerID, RunID: rc.RunID, NodeID: rc.CurrentNodeID,
			ToolID: def.ID, ToolName: def.Name, ToolType: def.ToolType,
			InputJSON: inputJSON, OutputJSON: outputJSON, Status: status,
			ErrorMessage: errMessage, LatencyMS: latencyMS,
		})
	}
	if callErr != nil {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ToolFailed, RunID: rc.RunID, Payload: map[string]any{"tool_id": def.ID, "error": errMessage, "latency_ms": latencyMS}})
		return nil, callErr
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ToolFinished, RunID: rc.RunID, Payload: map[string]any{"tool_id": def.ID, "status_code": output["status_code"], "latency_ms": latencyMS}})
	return output, nil
}

func ExecuteHTTPToolDefinition(ctx context.Context, def *tool.Definition, inputJSON []byte) (engine.NodeOutput, error) {
	output, err := toolruntime.ExecuteHTTPDefinition(ctx, def, inputJSON)
	return engine.NodeOutput(output), err
}
