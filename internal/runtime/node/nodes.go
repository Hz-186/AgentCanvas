package node

import (
	"context"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/engine"
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

type Deps struct {
	Retriever       retrieval.Retriever
	LLM             llm.ChatClient
	Providers       ProviderConfigLoader
	Messages        MessageWriter
	MessageHistory  MessageHistoryReader
	Memories        memory.Repository
	MemoryWriteLogs memory.WriteLogRepository
	Tools           tool.DefinitionRepository
	ToolInvocations tool.InvocationRepository
}

func DefaultNodes(deps Deps) []engine.Node {
	return []engine.Node{
		BeginNode{},
		RetrievalNode{Retriever: deps.Retriever},
		PromptNode{},
		LLMNode{Client: deps.LLM, Providers: deps.Providers, History: deps.MessageHistory},
		MessageNode{Writer: deps.Messages},
		MemoryReadNode{Memories: deps.Memories},
		MemoryWriteNode{Memories: deps.Memories, Logs: deps.MemoryWriteLogs},
		HTTPToolNode{Tools: deps.Tools, Invocations: deps.ToolInvocations},
		SwitchNode{},
		JSONOutputNode{},
		GuardrailNode{},
	}
}
