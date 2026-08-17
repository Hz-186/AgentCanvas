package agentruntime

import (
	"context"
	"os"
	"time"

	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/conversationcontext"
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

type MessageHistoryReader interface {
	ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
}

type PythonToolLoader interface {
	LoadRuntimeTools(ctx context.Context, allowed []string, invocations tool.InvocationRepository) ([]toolruntime.RuntimeTool, error)
}

type ArchivalIndexFactory interface {
	ForProvider(LoadedProvider) memory.ArchivalIndex
}

type ArchivalIndexFactoryFunc func(LoadedProvider) memory.ArchivalIndex

func (f ArchivalIndexFactoryFunc) ForProvider(provider LoadedProvider) memory.ArchivalIndex {
	return f(provider)
}

type Repositories struct {
	Retriever        retrieval.Retriever
	Providers        ProviderConfigLoader
	MessageHistory   MessageHistoryReader
	Compactions      conversation.CompactionRepository
	SessionSearch    conversation.MessageSearchIndex
	Memories         memory.Repository
	MemoryReader     MemoryBatchReader
	MemoryWriteLogs  memory.WriteLogRepository
	MemoryRecallLogs memory.RecallLogRepository
	MemoryCandidates memory.CandidateWriter
	MemoryRetriever  memory.SemanticRetriever
	WorkingMemory    memory.WorkingMemoryRepository
	ToolPacks        tool.PackRepository
	Skills           skill.Repository
	MCPServers       tool.MCPRepository
	ToolInvocations  tool.InvocationRepository
	ContextIndex     contextresource.Index
}

type RuntimeClients struct {
	LLM          llm.ChatClient
	ToolCalling  llm.ToolCallingClient
	Embedder     llm.EmbeddingClient
	PythonBridge PythonToolLoader
	Archival     ArchivalIndexFactory
}

type Tooling struct {
	ToolRegistry        toolruntime.Registry
	SubagentDispatcher  toolruntime.SubagentDispatcher
	PythonToolAllowlist []string
}

type Workspace struct {
	Sandbox sandbox.Runner
	Git     toolruntime.GitOperations
}

type Observability struct {
	Audits      audit.Repository
	Reflections reflection.Advisor
}

type Policies struct {
	MemoryExtractionTrigger func(ctx context.Context, ownerID int64, conversationID int64, roundNumber int)
	FileReadMaxChars        int
	MaxOutputBytes          int
	WorkspaceTimeout        time.Duration
}

type Deps struct {
	Repositories
	RuntimeClients
	Tooling
	Workspace
	Observability
	Policies
}

func buildRuntimeCore(deps Deps) runtimeCore {
	workspaceRoot, _ := os.Getwd()
	return runtimeCore{
		coreRepositories: coreRepositories{
			Providers: deps.Providers, ToolPacks: deps.ToolPacks, Skills: deps.Skills, MCPServers: deps.MCPServers,
			Retriever: deps.Retriever, MemoryRetriever: deps.MemoryRetriever, Memories: deps.Memories, MemoryReader: deps.MemoryReader,
			MemoryLogs: deps.MemoryWriteLogs, MemoryRecallLogs: deps.MemoryRecallLogs, MemoryCandidates: deps.MemoryCandidates,
			WorkingMemory: deps.WorkingMemory, MessageHistory: deps.MessageHistory, Compactions: deps.Compactions,
			SessionSearch: deps.SessionSearch, ContextIndex: deps.ContextIndex, ToolInvocations: deps.ToolInvocations,
		},
		coreClients: coreClients{LLM: deps.ToolCalling, Embedder: deps.Embedder, PythonBridge: deps.PythonBridge, Archival: deps.Archival},
		coreTooling: coreTooling{Tools: deps.ToolRegistry, SubagentDispatcher: deps.SubagentDispatcher,
			PythonToolAllowlist: append([]string(nil), deps.PythonToolAllowlist...)},
		coreWorkspace: coreWorkspace{Sandbox: deps.Sandbox, Coordinator: conversationCoordinator(deps), Git: deps.Git,
			FileReadMaxChars: deps.FileReadMaxChars, MaxOutputBytes: deps.MaxOutputBytes, WorkspaceTimeout: deps.WorkspaceTimeout, SkillRoot: workspaceRoot},
		coreObservability: coreObservability{Audits: deps.Audits, Reflections: deps.Reflections},
		corePolicies:      corePolicies{OnExtractTrigger: deps.MemoryExtractionTrigger},
	}
}

func conversationCoordinator(deps Deps) *conversationcontext.Coordinator {
	snapshots, ok := deps.Compactions.(conversation.SnapshotRepository)
	if !ok || deps.MessageHistory == nil || deps.LLM == nil {
		return nil
	}
	return &conversationcontext.Coordinator{History: deps.MessageHistory, Snapshots: snapshots, Client: deps.LLM}
}
