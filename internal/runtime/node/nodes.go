package node

import (
	"context"
	"fmt"
	"os"

	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/harness/rules"
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

type ActiveRuleSetLoader interface {
	LoadActiveRuleSet(ctx context.Context, ownerID, workflowID int64) (*rules.CompiledRuleSet, error)
}

type Deps struct {
	Retriever               retrieval.Retriever
	LLM                     llm.ChatClient
	Providers               ProviderConfigLoader
	Messages                MessageWriter
	MessageHistory          MessageHistoryReader
	Compactions             conversation.CompactionRepository
	SessionSearch           conversation.MessageSearchIndex
	Memories                memory.Repository
	MemoryReader            MemoryBatchReader
	MemoryWriteLogs         memory.WriteLogRepository
	MemoryRetriever         memory.SemanticRetriever
	WorkingMemory           memory.WorkingMemoryRepository
	MemoryExtractionTrigger func(ctx context.Context, ownerID int64, conversationID int64, roundNumber int)
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
	AgentCaller             toolruntime.AgentCaller
	Profiles                AgentProfileLoader
	RuleSets                ActiveRuleSetLoader
	Reflections             reflection.Advisor
	Teams                   workflow.TeamRepository
	Workspaces              workspace.Repository
	Sandbox                 sandbox.Runner
	ArchivalVecStore        vectorstore.Store
	ContextIndex            contextresource.Index
	Embedder                llm.EmbeddingClient
	SharedAgentRuntime      *SharedAgentRuntime
}

func DefaultNodes(deps Deps) ([]engine.Node, error) {
	if deps.ToolCalling == nil {
		return nil, fmt.Errorf("tool calling client is required")
	}
	if deps.Sandbox == nil {
		return nil, fmt.Errorf("sandbox runner is required")
	}
	agentRuntime := deps.SharedAgentRuntime
	if agentRuntime == nil {
		var err error
		agentRuntime, err = NewSharedAgentRuntime(deps)
		if err != nil {
			return nil, err
		}
	}
	agentNode := agentRuntime.node
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

func buildAgentNode(deps Deps) AgentNode {
	workspaceRoot, _ := os.Getwd()
	var workspaceManager *toolruntime.WorkspaceManager
	if deps.Workspaces != nil {
		workspaceManager = toolruntime.NewWorkspaceManager(deps.Workspaces)
	}
	return AgentNode{
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
		MemoryReader:      deps.MemoryReader,
		MemoryLogs:        deps.MemoryWriteLogs,
		WorkingMemory:     deps.WorkingMemory,
		WorkflowCaller:    deps.WorkflowCaller,
		InlineAgentCaller: deps.InlineAgentCaller,
		AgentCaller:       deps.AgentCaller,
		Profiles:          deps.Profiles,
		RuleSets:          deps.RuleSets,
		Reflections:       deps.Reflections,
		Workspaces:        deps.Workspaces,
		WorkspaceManager:  workspaceManager,
		Sandbox:           deps.Sandbox,
		MessageHistory:    deps.MessageHistory,
		Compactions:       deps.Compactions,
		SessionSearch:     deps.SessionSearch,
		ArchivalVecStore:  deps.ArchivalVecStore,
		ContextIndex:      deps.ContextIndex,
		Embedder:          deps.Embedder,
		WorkspaceRoot:     workspaceRoot,
		OnExtractTrigger:  deps.MemoryExtractionTrigger,
	}
}
