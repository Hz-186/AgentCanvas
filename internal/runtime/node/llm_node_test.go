package node

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

type compactionChatFake struct {
	calls int
	err   error
}

func (f *compactionChatFake) Chat(context.Context, llm.ChatProviderConfig, llm.ChatRequest) (*llm.ChatResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &llm.ChatResponse{Content: "goal and constraints preserved", Usage: llm.Usage{TotalTokens: 7}}, nil
}
func (*compactionChatFake) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

func TestLLMNodeCompactionCallsModelOnceAndKeepsRecentMessages(t *testing.T) {
	client := &compactionChatFake{}
	node := LLMNode{Client: client}
	messages := make([]llm.ChatMessage, 12)
	for i := range messages {
		messages[i] = llm.ChatMessage{Role: conversation.RoleUser, Content: "important historical context"}
	}
	compacted, usage, trace, err := node.compactMessages(context.Background(), llm.ChatProviderConfig{ProviderType: "openai_compatible"}, "gpt-4o", llmConfig{ModelAutoCompactTokenLimit: 1}, messages)
	if err != nil || client.calls != 1 || trace == nil || !trace.ModelCalled || len(compacted) != 9 || usage.TotalTokens != 7 {
		t.Fatalf("unexpected compaction: calls=%d len=%d usage=%+v trace=%+v err=%v", client.calls, len(compacted), usage, trace, err)
	}
}

func TestLLMNodeCompactionFallsBackDeterministically(t *testing.T) {
	client := &compactionChatFake{err: errors.New("timeout")}
	node := LLMNode{Client: client}
	messages := make([]llm.ChatMessage, 12)
	for i := range messages {
		messages[i] = llm.ChatMessage{Role: conversation.RoleUser, Content: "AgentCanvas 401 upgrade constraint"}
	}
	compacted, _, trace, err := node.compactMessages(context.Background(), llm.ChatProviderConfig{ProviderType: "openai_compatible"}, "gpt-4o", llmConfig{ModelAutoCompactTokenLimit: 1}, messages)
	if err != nil || trace == nil || trace.Status != "fallback" || len(compacted) != 9 || compacted[0].Content == "" {
		t.Fatalf("unexpected fallback: len=%d trace=%+v err=%v", len(compacted), trace, err)
	}
}
