package node

import (
	"context"

	"agentcanvas/internal/domain/retrieval"
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

type Deps struct {
	Retriever retrieval.Retriever
	LLM       llm.ChatClient
	Providers ProviderConfigLoader
	Messages  MessageWriter
}

func DefaultNodes(deps Deps) []engine.Node {
	return []engine.Node{
		BeginNode{},
		RetrievalNode{Retriever: deps.Retriever},
		PromptNode{},
		LLMNode{Client: deps.LLM, Providers: deps.Providers},
		MessageNode{Writer: deps.Messages},
	}
}
