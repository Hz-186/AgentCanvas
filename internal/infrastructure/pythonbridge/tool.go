package pythonbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/toolruntime"
)

type RuntimeTool struct {
	Client      *Client
	Capability  ToolCapability
	Invocations tool.InvocationRepository
}

// PythonRuntimeTool is the explicit name used by the bridge integration plan.
type PythonRuntimeTool = RuntimeTool

func (t RuntimeTool) Name() string        { return t.Capability.Name }
func (t RuntimeTool) Description() string { return t.Capability.Description }
func (t RuntimeTool) Parameters() json.RawMessage {
	if len(t.Capability.Parameters) == 0 {
		return json.RawMessage(`{"type":"object","additionalProperties":false}`)
	}
	return append(json.RawMessage(nil), t.Capability.Parameters...)
}

func (t RuntimeTool) Metadata() toolruntime.ToolMetadata {
	return toolruntime.ToolMetadata{
		RiskLevel:        normalizedRisk(t.Capability.RiskLevel),
		RequiresApproval: t.Capability.SideEffect != toolruntime.SideEffectNone,
		SideEffect:       normalizedSideEffect(t.Capability.SideEffect),
		ExecutionClass:   toolruntime.ExecutionSerial,
	}
}

func (t RuntimeTool) Execute(ctx context.Context, rc toolruntime.ToolRunContext, input json.RawMessage) (result *toolruntime.ToolResult, runErr error) {
	started := time.Now()
	defer func() {
		if t.Invocations == nil {
			return
		}
		output := json.RawMessage(`null`)
		if result != nil && json.Valid(result.ContentJSON) {
			output = result.ContentJSON
		}
		invocationStatus, errorMessage := tool.InvocationStatusSucceeded, ""
		if runErr != nil {
			invocationStatus, errorMessage = tool.InvocationStatusFailed, runErr.Error()
		} else if result != nil && result.IsError {
			invocationStatus, errorMessage = tool.InvocationStatusFailed, result.ContentText
		}
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = t.Invocations.Create(auditCtx, &tool.Invocation{
			ImmutableModel: domain.ImmutableModel{OwnerID: rc.OwnerID}, RunID: rc.RunID, AgentID: rc.AgentID, ToolName: t.Name(), ToolType: "python_bridge",
			InputJSON: input, OutputJSON: output, Status: invocationStatus, ErrorMessage: errorMessage,
			LatencyMS: int(time.Since(started).Milliseconds()),
		})
	}()
	if t.Client == nil {
		return nil, fmt.Errorf("python tool client is not configured")
	}
	if !json.Valid(input) {
		return nil, fmt.Errorf("python tool input must be valid JSON")
	}
	resp, err := t.Client.ExecuteTool(ctx, t.Name(), input, rc)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("python bridge returned an empty tool response")
	}
	if strings.TrimSpace(resp.ContentJson) != "" && !json.Valid([]byte(resp.ContentJson)) {
		return nil, fmt.Errorf("python tool returned invalid content JSON")
	}
	metadata := map[string]any{}
	if strings.TrimSpace(resp.MetadataJson) != "" {
		if err := json.Unmarshal([]byte(resp.MetadataJson), &metadata); err != nil {
			return nil, fmt.Errorf("decode Python tool metadata: %w", err)
		}
	}
	return &toolruntime.ToolResult{ContentJSON: json.RawMessage(resp.ContentJson), ContentText: resp.ContentText, IsError: resp.IsError, Metadata: metadata}, nil
}

func normalizedRisk(value string) string {
	switch strings.TrimSpace(value) {
	case toolruntime.RiskLow:
		return toolruntime.RiskLow
	case toolruntime.RiskMedium:
		return toolruntime.RiskMedium
	case toolruntime.RiskHigh:
		return toolruntime.RiskHigh
	default:
		return toolruntime.RiskHigh
	}
}

func normalizedSideEffect(value string) string {
	switch strings.TrimSpace(value) {
	case toolruntime.SideEffectNone:
		return toolruntime.SideEffectNone
	case toolruntime.SideEffectRead, toolruntime.SideEffectWrite, toolruntime.SideEffectExternalAction:
		return value
	default:
		return toolruntime.SideEffectExternalAction
	}
}

func RuntimeTools(client *Client, capabilities *Capabilities, allowed []string, invocationRepositories ...tool.InvocationRepository) []toolruntime.RuntimeTool {
	if client == nil || capabilities == nil {
		return nil
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allow[strings.TrimSpace(name)] = struct{}{}
	}
	if len(allow) == 0 {
		return nil
	}
	var invocations tool.InvocationRepository
	if len(invocationRepositories) > 0 {
		invocations = invocationRepositories[0]
	}
	tools := make([]toolruntime.RuntimeTool, 0, len(capabilities.Tools))
	for _, capability := range capabilities.Tools {
		if _, ok := allow[capability.Name]; !ok {
			continue
		}
		tools = append(tools, RuntimeTool{Client: client, Capability: capability, Invocations: invocations})
	}
	return tools
}

// LoadRuntimeTools is the runtime-facing port implemented by the bridge
// adapter; callers do not need the concrete client or capability DTO.
func (c *Client) LoadRuntimeTools(ctx context.Context, allowed []string, invocations tool.InvocationRepository) ([]toolruntime.RuntimeTool, error) {
	capabilities, err := c.GetCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	return RuntimeTools(c, capabilities, allowed, invocations), nil
}
