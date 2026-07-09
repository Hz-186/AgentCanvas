package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type MCPToolNode struct {
	Servers tool.MCPRepository
}

type mcpToolConfig struct {
	ServerID int64           `json:"server_id"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

func (MCPToolNode) Type() string { return "mcp_tool" }

func (MCPToolNode) Validate(config json.RawMessage) error {
	var cfg mcpToolConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid mcp_tool config", agenterrors.ErrInvalidInput)
	}
	if cfg.ServerID <= 0 {
		return fmt.Errorf("%w: mcp_tool server_id is required", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.ToolName) == "" {
		return fmt.Errorf("%w: mcp_tool tool_name is required", agenterrors.ErrInvalidInput)
	}
	if len(cfg.Input) > 0 && string(cfg.Input) != "null" {
		var input any
		if err := json.Unmarshal(cfg.Input, &input); err != nil {
			return fmt.Errorf("%w: mcp_tool input must be valid JSON", agenterrors.ErrInvalidInput)
		}
	}
	return nil
}

func (n MCPToolNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Servers == nil {
		return nil, fmt.Errorf("mcp_tool server repository is not configured")
	}
	var cfg mcpToolConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("%w: invalid mcp_tool config", agenterrors.ErrInvalidInput)
	}
	server, err := n.Servers.FindServerByID(ctx, rc.OwnerID, cfg.ServerID)
	if err != nil {
		return nil, err
	}
	if server.Status != tool.MCPStatusActive {
		return nil, fmt.Errorf("%w: mcp server is disabled", agenterrors.ErrForbidden)
	}
	resolvedInput := cfg.Input
	if len(resolvedInput) == 0 || string(resolvedInput) == "null" {
		resolvedInput = json.RawMessage(`{}`)
	}
	resolvedInput = []byte(engine.ResolveTemplate(string(resolvedInput), rc))
	client := toolruntime.NewMCPClientFromServer(server)
	result, err := client.CallTool(ctx, strings.TrimSpace(cfg.ToolName), resolvedInput)
	if err != nil {
		return nil, err
	}
	output := engine.NodeOutput{
		"server_id":    cfg.ServerID,
		"tool_name":    strings.TrimSpace(cfg.ToolName),
		"content_text": result.ContentText,
		"is_error":     result.IsError,
	}
	if len(result.ContentJSON) > 0 {
		output["content_json"] = json.RawMessage(result.ContentJSON)
	}
	if result.Metadata != nil {
		output["metadata"] = result.Metadata
	}
	return output, nil
}
