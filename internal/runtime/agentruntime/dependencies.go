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
	gitinfra "agentcanvas/internal/infrastructure/git"
	"agentcanvas/internal/infrastructure/llm"
	pythonbridgeinfra "agentcanvas/internal/infrastructure/pythonbridge"
	"agentcanvas/internal/infrastructure/vectorstore"
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

type MessageWriter interface {
	WriteAssistantMessage(ctx context.Context, ownerID int64, conversationID *int64, runID int64, content string, tokenCount int) (int64, error)
}

type MessageHistoryReader interface {
	ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
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
	MemoryRecallLogs        memory.RecallLogRepository
	MemoryCommands          memory.Commander
	MemoryCandidates        memory.CandidateWriter
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
	SubagentDispatcher      toolruntime.SubagentDispatcher
	Reflections             reflection.Advisor
	Sandbox                 sandbox.Runner
	ArchivalVecStore        vectorstore.Store
	ContextIndex            contextresource.Index
	Embedder                llm.EmbeddingClient
	Git                     *gitinfra.Service
	PythonBridge            *pythonbridgeinfra.Client
	PythonToolAllowlist     []string
	FileReadMaxChars        int
	MaxOutputBytes          int
	WorkspaceTimeout        time.Duration
}

func buildRuntimeCore(deps Deps) runtimeCore {
	workspaceRoot, _ := os.Getwd()
	return runtimeCore{
		LLM:                 deps.ToolCalling,
		Providers:           deps.Providers,
		Tools:               deps.ToolRegistry,
		ToolPacks:           deps.ToolPacks,
		Skills:              deps.Skills,
		Audits:              deps.Audits,
		MCPServers:          deps.MCPServers,
		Retriever:           deps.Retriever,
		MemoryRetriever:     deps.MemoryRetriever,
		Memories:            deps.Memories,
		MemoryReader:        deps.MemoryReader,
		MemoryLogs:          deps.MemoryWriteLogs,
		MemoryRecallLogs:    deps.MemoryRecallLogs,
		MemoryCandidates:    deps.MemoryCandidates,
		WorkingMemory:       deps.WorkingMemory,
		SubagentDispatcher:  deps.SubagentDispatcher,
		Reflections:         deps.Reflections,
		Sandbox:             deps.Sandbox,
		MessageHistory:      deps.MessageHistory,
		Coordinator:         conversationCoordinator(deps),
		SessionSearch:       deps.SessionSearch,
		ArchivalVecStore:    deps.ArchivalVecStore,
		ContextIndex:        deps.ContextIndex,
		Embedder:            deps.Embedder,
		Git:                 deps.Git,
		PythonBridge:        deps.PythonBridge,
		PythonToolAllowlist: append([]string(nil), deps.PythonToolAllowlist...),
		ToolInvocations:     deps.ToolInvocations,
		FileReadMaxChars:    deps.FileReadMaxChars,
		MaxOutputBytes:      deps.MaxOutputBytes,
		WorkspaceTimeout:    deps.WorkspaceTimeout,
		SkillRoot:           workspaceRoot,
		OnExtractTrigger:    deps.MemoryExtractionTrigger,
	}
}

func conversationCoordinator(deps Deps) *conversationcontext.Coordinator {
	snapshots, ok := deps.Compactions.(conversation.SnapshotRepository)
	if !ok || deps.MessageHistory == nil || deps.LLM == nil {
		return nil
	}
	return &conversationcontext.Coordinator{History: deps.MessageHistory, Snapshots: snapshots, Client: deps.LLM}
}
