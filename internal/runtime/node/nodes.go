package node

import (
	"context"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"
)

type ProviderConfigLoader interface {
	LoadChatProviderConfig(ctx context.Context, ownerID, providerID int64, model string) (*LoadedProvider, error)
}

type LoadedProvider struct {
	ProviderID int64
	Model      string
	Config     llm.ChatProviderConfig
}

type MessageWriter interface {
	WriteAssistantMessage(ctx context.Context, ownerID int64, conversationID *int64, runID int64, content string, tokenCount int) (int64, error)
}

type MessageHistoryReader interface {
	ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
}

type AgentProfileLoader interface {
	GetWorkflowProfile(ctx context.Context, ownerID, workflowID int64) (*workflow.Profile, error)
}

type Deps struct {
	Retriever       retrieval.Retriever
	LLM             llm.ChatClient
	Providers       ProviderConfigLoader
	Messages        MessageWriter
	MessageHistory  MessageHistoryReader
	Memories        memory.Repository
	MemoryWriteLogs memory.WriteLogRepository
	Tools           tool.DefinitionRepository
	ToolPacks       tool.PackRepository
	MCPServers      tool.MCPRepository
	ToolInvocations tool.InvocationRepository
	ToolCalling     llm.ToolCallingClient
	ToolRegistry    toolruntime.Registry
	WorkflowCaller  toolruntime.WorkflowCaller
	Profiles        AgentProfileLoader
	Teams           workflow.TeamRepository
	Sandbox         sandbox.Runner
}

func DefaultNodes(deps Deps) []engine.Node {
	toolCalling := deps.ToolCalling
	if toolCalling == nil {
		if client, ok := deps.LLM.(llm.ToolCallingClient); ok {
			toolCalling = client
		}
	}
	toolRegistry := deps.ToolRegistry
	if toolRegistry == nil && deps.Tools != nil {
		toolRegistry = toolruntime.BasicRegistry{Tools: deps.Tools, Invocations: deps.ToolInvocations}
	}
	sandboxRunner := deps.Sandbox
	if sandboxRunner == nil {
		defaultRunner := sandbox.NewDockerRunner()
		sandboxRunner = defaultRunner
	}
	agentNode := AgentNode{LLM: toolCalling, Providers: deps.Providers, Tools: toolRegistry, ToolPacks: deps.ToolPacks, Retriever: deps.Retriever, Memories: deps.Memories, MemoryLogs: deps.MemoryWriteLogs, WorkflowCaller: deps.WorkflowCaller, Profiles: deps.Profiles, Sandbox: sandboxRunner, MessageHistory: deps.MessageHistory}
	return []engine.Node{
		BeginNode{},
		RetrievalNode{Retriever: deps.Retriever},
		PromptNode{},
		LLMNode{Client: deps.LLM, Providers: deps.Providers, History: deps.MessageHistory},
		AgentLoopNode{AgentNode: agentNode},
		WorkflowCallNode{Caller: deps.WorkflowCaller},
		TeamCallNode{Teams: deps.Teams, Caller: deps.WorkflowCaller},
		CodeSandboxNode{Runner: sandboxRunner},
		MessageNode{Writer: deps.Messages},
		MemoryReadNode{Memories: deps.Memories},
		MemoryWriteNode{Memories: deps.Memories, Logs: deps.MemoryWriteLogs},
		HTTPToolNode{Tools: deps.Tools, Invocations: deps.ToolInvocations},
		MCPToolNode{Servers: deps.MCPServers},
		SwitchNode{},
		JSONOutputNode{},
		GuardrailNode{},
	}
}
