package node

import (
	"context"
	"fmt"
	"os"

	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"
)

type ProviderConfigLoader interface {
	LoadChatProviderConfig(ctx context.Context, ownerID, providerID int64, model string) (*LoadedProvider, error)
}

type LoadedProvider struct {
	ProviderID      int64
	Model           string
	Config          llm.ChatProviderConfig
	EmbeddingConfig llm.EmbeddingProviderConfig
	EmbeddingModel  string
}

type MessageWriter interface {
	WriteAssistantMessage(ctx context.Context, ownerID int64, conversationID *int64, runID int64, content string, tokenCount int) (int64, error)
}

type MessageHistoryReader interface {
	ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
}

type AgentProfileLoader interface {
	GetWorkflowProfile(ctx context.Context, ownerID, workflowID int64) (*workflow.Profile, error)
}

type Deps struct {
	Retriever               retrieval.Retriever
	LLM                     llm.ChatClient
	Providers               ProviderConfigLoader
	Messages                MessageWriter
	MessageHistory          MessageHistoryReader
	Memories                memory.Repository
	MemoryWriteLogs         memory.WriteLogRepository
	MemoryRetriever         memory.SemanticRetriever
	WorkingMemory           memory.WorkingMemoryRepository
	MemoryExtractionTrigger func(ctx context.Context, ownerID int64, conversationID int64)
	Tools                   tool.DefinitionRepository
	ToolPacks               tool.PackRepository
	Skills                  skill.Repository
	Audits                  audit.Repository
	MCPServers              tool.MCPRepository
	ToolInvocations         tool.InvocationRepository
	ToolCalling             llm.ToolCallingClient
	ToolRegistry            toolruntime.Registry
	WorkflowCaller          toolruntime.WorkflowCaller
	InlineAgentCaller       toolruntime.InlineAgentCaller
	Profiles                AgentProfileLoader
	Teams                   workflow.TeamRepository
	Sandbox                 sandbox.Runner
	ArchivalVecStore        vectorstore.Store
	Embedder                llm.EmbeddingClient
}

func DefaultNodes(deps Deps) ([]engine.Node, error) {
	if deps.ToolCalling == nil {
		return nil, fmt.Errorf("tool calling client is required")
	}
	if deps.Sandbox == nil {
		return nil, fmt.Errorf("sandbox runner is required")
	}
	workspaceRoot, _ := os.Getwd()
	agentNode := AgentNode{
		LLM:               deps.ToolCalling,
		Providers:         deps.Providers,
		Tools:             deps.ToolRegistry,
		ToolPacks:         deps.ToolPacks,
		Skills:            deps.Skills,
		Audits:            deps.Audits,
		MCPServers:        deps.MCPServers,
		Retriever:         deps.Retriever,
		MemoryRetriever:   deps.MemoryRetriever,
		Memories:          deps.Memories,
		MemoryLogs:        deps.MemoryWriteLogs,
		WorkingMemory:     deps.WorkingMemory,
		WorkflowCaller:    deps.WorkflowCaller,
		InlineAgentCaller: deps.InlineAgentCaller,
		Profiles:          deps.Profiles,
		Sandbox:           deps.Sandbox,
		MessageHistory:    deps.MessageHistory,
		ArchivalVecStore:  deps.ArchivalVecStore,
		Embedder:          deps.Embedder,
		WorkspaceRoot:     workspaceRoot,
		OnExtractTrigger:  deps.MemoryExtractionTrigger,
	}
	return []engine.Node{
		BeginNode{},
		RetrievalNode{Retriever: deps.Retriever},
		PromptNode{},
		LLMNode{Client: deps.LLM, Providers: deps.Providers, History: deps.MessageHistory},
		AgentLoopNode{AgentNode: agentNode},
		NewAgentCallNode(deps.WorkflowCaller),
		NewWorkflowCallNode(deps.WorkflowCaller),
		TeamCallNode{Teams: deps.Teams, Caller: deps.WorkflowCaller},
		CodeSandboxNode{Runner: deps.Sandbox},
		MessageNode{Writer: deps.Messages},
		MemoryReadNode{Memories: deps.Memories, Retriever: deps.MemoryRetriever},
		MemoryWriteNode{Memories: deps.Memories, Logs: deps.MemoryWriteLogs, Retriever: deps.MemoryRetriever},
		HTTPToolNode{Tools: deps.Tools, Invocations: deps.ToolInvocations},
		MCPToolNode{Servers: deps.MCPServers},
		SwitchNode{},
		JSONOutputNode{},
		GuardrailNode{},
	}, nil
}
