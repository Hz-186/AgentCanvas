package node

import (
	"context"

	"agentcanvas/internal/runtime/toolruntime"
)

// AgentResourceResolver owns runtime resource materialization. Keeping this
// boundary separate from the workflow node makes resource loading reusable by
// independent Agent adapters and leaves the node focused on orchestration.
type AgentResourceResolver struct {
	node AgentNode
}

func NewAgentResourceResolver(node AgentNode) AgentResourceResolver {
	return AgentResourceResolver{node: node}
}

func (r AgentResourceResolver) ResolveTools(ctx context.Context, ownerID int64, cfg agentRuntimeConfig, provider *LoadedProvider) ([]toolruntime.RuntimeTool, error) {
	return r.node.loadTools(ctx, ownerID, cfg, provider)
}
