package agent

import (
	"context"
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

func TestCompactInitialHistoryCallsModelOnceAndPreservesRecentTurns(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "Goal: diagnose AgentCanvas 401\nConstraints: version v2.1"}, Usage: llm.Usage{TotalTokens: 11}}}}
	runner := &Runner{LLM: client}
	blocks := make([]ContextBlock, 0, 7)
	for index := 0; index < 7; index++ {
		blocks = append(blocks, ContextBlock{Name: "conversation", Role: conversation.RoleUser, Content: strings.Repeat("AgentCanvas 401 v2.1 detail ", 12)})
	}
	req := RunRequest{Model: "test-model", Task: "diagnose", SystemPrompt: "mandatory", ContextBlocks: blocks, ModelAutoCompactTokenLimit: 80, ModelAutoCompactTokenLimitScope: "total"}
	compacted, usage, trace := runner.compactInitialHistory(context.Background(), req, nil)
	if len(client.requests) != 1 || trace == nil || trace.Status != "completed" || usage.TotalTokens != 11 {
		t.Fatalf("unexpected compaction: calls=%d trace=%+v usage=%+v", len(client.requests), trace, usage)
	}
	if len(compacted) != 5 || compacted[0].Name != "history_model_summary" {
		t.Fatalf("expected one summary plus four recent turns, got %+v", compacted)
	}
}

func TestCompactInitialHistoryDoesNotRunBelowThreshold(t *testing.T) {
	client := &fakeToolClient{}
	runner := &Runner{LLM: client}
	blocks := []ContextBlock{{Name: "conversation", Role: conversation.RoleUser, Content: "short"}}
	compacted, _, trace := runner.compactInitialHistory(context.Background(), RunRequest{Model: "test", Task: "task", ContextBlocks: blocks, ModelAutoCompactTokenLimit: 1000}, nil)
	if len(client.requests) != 0 || trace != nil || len(compacted) != 1 {
		t.Fatalf("unexpected below-threshold compaction: calls=%d trace=%+v", len(client.requests), trace)
	}
}

func TestInitialCompactionKeepsFourCompleteDialogueTurns(t *testing.T) {
	blocks := make([]ContextBlock, 0, 10)
	for index := 0; index < 5; index++ {
		blocks = append(blocks, ContextBlock{Name: "conversation", Role: conversation.RoleUser, Content: "question"}, ContextBlock{Name: "conversation", Role: conversation.RoleAssistant, Content: "answer"})
	}
	indexes := make([]int, len(blocks))
	for index := range indexes {
		indexes[index] = index
	}
	older := historyIndexesBeforeRecentTurns(blocks, indexes, 4)
	if len(older) != 2 {
		t.Fatalf("expected one complete old turn, got indexes=%v", older)
	}
}

func TestCompactInitialHistoryFallsBackWithoutDroppingHistory(t *testing.T) {
	client := &failingCompactionClient{}
	runner := &Runner{LLM: client}
	blocks := make([]ContextBlock, 0, 6)
	for index := 0; index < 6; index++ {
		blocks = append(blocks, ContextBlock{Name: "conversation", Role: conversation.RoleUser, Content: strings.Repeat("history ", 20)})
	}
	compacted, _, trace := runner.compactInitialHistory(context.Background(), RunRequest{Model: "test", Task: "task", ContextBlocks: blocks, ModelAutoCompactTokenLimit: 20}, nil)
	if trace == nil || trace.Status != "fallback" || len(compacted) != len(blocks) {
		t.Fatalf("fallback must preserve input for deterministic compressor: trace=%+v blocks=%d", trace, len(compacted))
	}
}

type failingCompactionClient struct{}

func (*failingCompactionClient) ChatWithTools(context.Context, llm.ChatProviderConfig, llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	return nil, context.DeadlineExceeded
}
