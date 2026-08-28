package agent

import (
	"context"
	"log/slog"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/logger"
	"agentcanvas/internal/pkg/observability"
)

func compactionDiagnosticsContext() context.Context {
	return observability.WithCorrelation(context.Background(), observability.Correlation{}.
		WithRequestID("rid-compact-1").
		WithOwnerID(3).
		WithConversationID(20).
		WithRunID(401).
		WithTurnID(201))
}

func TestCompactionDiagnosticsLogsCompactionSummary(t *testing.T) {
	conversationID := int64(20)
	newRequest := func() RunRequest {
		return RunRequest{
			Provider:       llm.ChatProviderConfig{ProviderType: "openai_compatible"},
			Model:          "gpt-4o",
			Task:           "task",
			ConversationID: &conversationID,
			RunID:          401,
		}
	}
	transcript := []llm.ChatMessage{
		{Role: conversation.RoleAssistant, Content: "sensitive-history-body"},
		{Role: conversation.RoleTool, Content: "result"},
	}

	t.Run("summarizer", func(t *testing.T) {
		client := &fakeToolClient{responses: []llm.ToolChatResponse{
			{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "summary: compacted"}, Usage: llm.Usage{PromptTokens: 11, CompletionTokens: 13, TotalTokens: 24}},
		}}
		captured := &diagnosticsCapturingHandler{}
		runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}

		compacted, _, trace := runner.compactRuntimeTranscript(compactionDiagnosticsContext(), newRequest(), transcript)
		if trace == nil || trace.Status != "completed" || len(compacted) == 0 {
			t.Fatalf("summarizer compaction must complete: trace=%+v compacted=%+v", trace, compacted)
		}
		events := captured.eventsNamed("compaction.completed")
		if len(events) != 1 {
			t.Fatalf("expected exactly one compaction.completed event, got %d", len(events))
		}
		for key, value := range map[string]any{
			"event":           "compaction.completed",
			"phase":           "compaction",
			"result":          "ok",
			"conversation_id": int64(20),
			"run_id":          int64(401),
			"request_id":      "rid-compact-1",
		} {
			if events[0].attrs[key] != value {
				t.Fatalf("compaction.completed attribute %q = %#v, want %#v", key, events[0].attrs[key], value)
			}
		}
		if latencyMS, ok := events[0].attrs["latency_ms"].(int64); !ok || latencyMS < 0 {
			t.Fatalf("compaction.completed latency_ms = %#v, want non-negative int", events[0].attrs["latency_ms"])
		}
		usage, ok := events[0].attrs["usage"].(map[string]int)
		if !ok {
			t.Fatalf("compaction.completed usage summary missing: %#v", events[0].attrs)
		}
		if usage["prompt_tokens"] != 11 || usage["completion_tokens"] != 13 || usage["total_tokens"] != 24 {
			t.Fatalf("compaction usage token summary = %#v, want 11/13/24", usage)
		}
		if usage["before_tokens"] <= 0 || usage["after_tokens"] < 0 || usage["saved_tokens"] < 0 {
			t.Fatalf("compaction token summary must carry before/after/saved counts: %#v", usage)
		}
		if captured.containsValue("sensitive-history-body") || captured.containsValue("compacted") {
			t.Fatal("compaction diagnostics leaked history or summary text")
		}
	})

	t.Run("token budget", func(t *testing.T) {
		client := &fakeToolClient{}
		captured := &diagnosticsCapturingHandler{}
		runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}
		request := newRequest()
		request.TokenBudgetCompaction = true

		_, _, trace := runner.compactRuntimeTranscript(compactionDiagnosticsContext(), request, transcript)
		if trace == nil || trace.Status != "completed" || trace.ModelCalled {
			t.Fatalf("token-budget compaction must complete without a model call: trace=%+v", trace)
		}
		events := captured.eventsNamed("compaction.completed")
		if len(events) != 1 {
			t.Fatalf("expected exactly one compaction.completed event, got %d", len(events))
		}
		if events[0].attrs["result"] != "ok" || events[0].attrs["conversation_id"] != int64(20) || events[0].attrs["run_id"] != int64(401) {
			t.Fatalf("token-budget compaction event mismatch: %#v", events[0].attrs)
		}
		usage, ok := events[0].attrs["usage"].(map[string]int)
		if !ok || usage["total_tokens"] != 0 || usage["before_tokens"] <= 0 {
			t.Fatalf("token-budget usage summary must stay zero-model with before tokens: %#v", events[0].attrs["usage"])
		}
	})

	t.Run("summarizer failure", func(t *testing.T) {
		client := &fakeToolClient{errs: []error{
			&modelTurnProviderError{message: "summarizer unavailable"},
			&modelTurnProviderError{message: "summarizer unavailable"},
			&modelTurnProviderError{message: "summarizer unavailable"},
		}}
		captured := &diagnosticsCapturingHandler{}
		runner := &Runner{LLM: client, Logger: slog.New(logger.NewDiagnosticsHandler(captured))}

		_, _, trace := runner.compactRuntimeTranscript(compactionDiagnosticsContext(), newRequest(), transcript)
		if trace == nil || trace.Status != "failed" {
			t.Fatalf("summarizer failure must keep the failed trace status: trace=%+v", trace)
		}
		events := captured.eventsNamed("compaction.completed")
		if len(events) != 1 {
			t.Fatalf("expected exactly one compaction.completed event, got %d", len(events))
		}
		if events[0].attrs["result"] != "error" {
			t.Fatalf("failed compaction event result = %#v, want error", events[0].attrs["result"])
		}
		errorClass, _ := events[0].attrs["error_class"].(string)
		if errorClass == "" {
			t.Fatalf("failed compaction event must carry an error_class: %#v", events[0].attrs)
		}
		if captured.containsValue("summarizer unavailable") || captured.containsValue("sensitive-history-body") {
			t.Fatal("compaction diagnostics leaked error text or history content")
		}
	})
}
