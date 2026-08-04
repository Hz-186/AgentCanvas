package agentruntime

import (
	"context"

	"agentcanvas/internal/runtime/toolruntime"
)

// AgentResourceResolver owns runtime resource materialization and keeps it
// reusable across Agent adapters.
type AgentResourceResolver struct {
	runtime runtimeCore
}

func NewAgentResourceResolver(runtime runtimeCore) AgentResourceResolver {
	return AgentResourceResolver{runtime: runtime}
}

func (r AgentResourceResolver) ResolveTools(ctx context.Context, ownerID int64, cfg agentRuntimeConfig, provider *LoadedProvider) ([]toolruntime.RuntimeTool, error) {
	return r.runtime.loadTools(ctx, ownerID, cfg, provider)
}
