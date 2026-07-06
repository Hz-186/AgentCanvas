package agent

import (
	"strings"
	"testing"
)

func TestContextAssemblerKeepsPinnedBlocksAndOmitsOverflow(t *testing.T) {
	messages, trace := ContextAssembler{MaxChars: 40}.Build(RunRequest{
		SystemPrompt: "system",
		Task:         "task",
		ContextBlocks: []ContextBlock{
			{Name: "history", Role: "user", Content: strings.Repeat("h", 80)},
			{Name: "memory", Role: "user", Content: "memory"},
		},
	})
	if len(messages) != 3 {
		t.Fatalf("expected system, memory and task messages, got %+v", messages)
	}
	if messages[0].Content != "system" || messages[len(messages)-1].Content != "task" {
		t.Fatalf("pinned messages not preserved: %+v", messages)
	}
	if len(trace.Omitted) != 1 || trace.Omitted[0] != "history" {
		t.Fatalf("unexpected trace: %+v", trace)
	}
	if trace.Strategy == "" || trace.UsedChars <= 0 {
		t.Fatalf("missing trace metadata: %+v", trace)
	}
}

func TestContextAssemblerTruncatesOversizedPinnedTask(t *testing.T) {
	messages, trace := ContextAssembler{MaxChars: 8}.Build(RunRequest{
		Task: strings.Repeat("x", 20),
	})
	if len(messages) != 1 {
		t.Fatalf("expected task message, got %+v", messages)
	}
	if len(messages[0].Content) != 8 {
		t.Fatalf("expected truncated content, got %q", messages[0].Content)
	}
	if len(trace.Truncated) != 1 || trace.Truncated[0] != "task" {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestContextAssemblerOmitsEmptyBlocks(t *testing.T) {
	messages, trace := ContextAssembler{MaxChars: 500}.Build(RunRequest{
		Task: "task",
		ContextBlocks: []ContextBlock{
			{Name: "empty", Role: "user", Content: ""},
			{Name: "valid", Role: "user", Content: "content"},
		},
	})
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (task + valid), got %+v", messages)
	}
	if len(trace.Included) == 0 {
		t.Fatal("expected included blocks")
	}
}

func TestContextAssemblerModeInstructions(t *testing.T) {
	cases := []struct {
		mode              string
		reflectionEnabled bool
		expectInstruction bool
	}{
		{"react", false, false},
		{"react", true, true},
		{"plan_execute", false, true},
		{"reflect", false, true},
		{"supervisor", false, true},
		{"unknown", false, false},
	}
	for _, tc := range cases {
		messages, _ := ContextAssembler{MaxChars: 5000}.Build(RunRequest{
			SystemPrompt:      "system",
			Task:              "task",
			Mode:              tc.mode,
			ReflectionEnabled: tc.reflectionEnabled,
		})
		hasModeInstruction := false
		for _, m := range messages {
			if m.Role == "system" && m.Content != "system" {
				hasModeInstruction = true
			}
		}
		if hasModeInstruction != tc.expectInstruction {
			t.Errorf("mode=%s reflection=%v: expected instruction=%v", tc.mode, tc.reflectionEnabled, tc.expectInstruction)
		}
	}
}

func TestContextAssemblerUsesDefaultMaxChars(t *testing.T) {
	_, trace := ContextAssembler{}.Build(RunRequest{
		Task: "task",
	})
	if trace.MaxChars != defaultMaxInputChars {
		t.Fatalf("expected default max chars %d, got %d", defaultMaxInputChars, trace.MaxChars)
	}
}

func TestContextAssemblerRespectsRequestMaxInputChars(t *testing.T) {
	_, trace := ContextAssembler{MaxChars: 10000}.Build(RunRequest{
		Task:          "task",
		MaxInputChars: 500,
	})
	if trace.MaxChars != 500 {
		t.Fatalf("expected 500 max chars, got %d", trace.MaxChars)
	}
}

func TestContextAssemblerAddsConversationContextBlocks(t *testing.T) {
	messages, trace := ContextAssembler{MaxChars: 5000}.Build(RunRequest{
		SystemPrompt: "system",
		Task:         "task",
		ContextBlocks: []ContextBlock{
			{Name: "conversation", Role: "user", Content: "previous user message", Pinned: false},
			{Name: "conversation", Role: "assistant", Content: "previous assistant response", Pinned: false},
		},
	})
	userFound := false
	assistantFound := false
	for _, m := range messages {
		if m.Role == "user" && m.Content == "previous user message" {
			userFound = true
		}
		if m.Role == "assistant" && m.Content == "previous assistant response" {
			assistantFound = true
		}
	}
	if !userFound || !assistantFound {
		t.Fatalf("conversation history not included: %+v", trace)
	}
}

func TestContextAssemblerBuildsTokenAuditByCategory(t *testing.T) {
	_, trace := ContextAssembler{MaxChars: 5000}.Build(RunRequest{
		SystemPrompt: "system prompt text",
		Task:         "answer the task",
		ContextBlocks: []ContextBlock{
			{Name: "conversation", Role: "user", Content: "previous turn"},
			{Name: "memory", Role: "user", Content: "remembered preference"},
			{Name: "retrieval:kb", Role: "user", Content: "knowledge chunk content"},
			{Name: "rules_l2:rag", Role: "system", Content: "cite retrieved chunks"},
		},
	})
	if trace.TokenAudit.Total != trace.EstimatedTokens {
		t.Fatalf("token audit total = %d, estimated = %d", trace.TokenAudit.Total, trace.EstimatedTokens)
	}
	if trace.TokenAudit.System == 0 || trace.TokenAudit.History == 0 || trace.TokenAudit.Memory == 0 || trace.TokenAudit.Retrieval == 0 || trace.TokenAudit.Task == 0 || trace.TokenAudit.RulesL2 == 0 {
		t.Fatalf("missing token audit categories: %+v", trace.TokenAudit)
	}
}

func TestContextAssemblerUsesTokenBudgetAndKeepsTask(t *testing.T) {
	messages, trace := ContextAssembler{}.Build(RunRequest{
		SystemPrompt:   "system",
		Task:           "must answer this task",
		MaxInputTokens: 20,
		ContextBlocks: []ContextBlock{
			{Name: "retrieval:large", Role: "user", Content: strings.Repeat("retrieval ", 200)},
		},
	})
	if trace.MaxInputTokens != 20 || trace.UsedTokens > 20 {
		t.Fatalf("expected token budget to be enforced, got %+v", trace)
	}
	if len(trace.Omitted) != 1 || trace.Omitted[0] != "retrieval:large" {
		t.Fatalf("expected retrieval block to be omitted, got %+v", trace)
	}
	if messages[len(messages)-1].Content != "must answer this task" {
		t.Fatalf("task should be preserved under budget pressure: %+v", messages)
	}
}

func TestContextAssemblerSummarizesOldHistoryAndDedupesRetrieval(t *testing.T) {
	blocks := []ContextBlock{
		{Name: "conversation", Role: "user", Content: "turn 1 " + strings.Repeat("old ", 20)},
		{Name: "conversation", Role: "assistant", Content: "turn 2 " + strings.Repeat("old ", 20)},
		{Name: "conversation", Role: "user", Content: "turn 3 " + strings.Repeat("old ", 20)},
		{Name: "conversation", Role: "assistant", Content: "turn 4 " + strings.Repeat("old ", 20)},
		{Name: "conversation", Role: "user", Content: "turn 5 recent"},
		{Name: "conversation", Role: "assistant", Content: "turn 6 recent"},
		{Name: "retrieval:1", Role: "user", Content: "same chunk content"},
		{Name: "retrieval:2", Role: "user", Content: " same   chunk   content "},
	}
	messages, trace := ContextAssembler{MaxInputTokens: 2000}.Build(RunRequest{Task: "task", ContextBlocks: blocks})
	if trace.SavedTokens == 0 || len(trace.Compressed) == 0 {
		t.Fatalf("expected compression to save tokens, got %+v", trace)
	}
	foundSummary := false
	for _, message := range messages {
		if strings.Contains(message.Content, "Earlier conversation summary") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Fatalf("expected old history summary in messages: %+v", messages)
	}
	duplicateOmitted := false
	for _, omitted := range trace.Omitted {
		if omitted == "retrieval:2" {
			duplicateOmitted = true
		}
	}
	if !duplicateOmitted {
		t.Fatalf("expected duplicate retrieval chunk to be omitted: %+v", trace)
	}
}

func TestContextAssemblerImprovesUsableSpaceIn32KWindow(t *testing.T) {
	blocks := make([]ContextBlock, 0, 80)
	for i := 0; i < 40; i++ {
		blocks = append(blocks, ContextBlock{Name: "conversation", Role: "user", Content: strings.Repeat("old history detail ", 200)})
	}
	for i := 0; i < 20; i++ {
		blocks = append(blocks, ContextBlock{Name: "retrieval:duplicate", Role: "user", Content: strings.Repeat("duplicate retrieved paragraph ", 80)})
	}
	for i := 0; i < 20; i++ {
		blocks = append(blocks, ContextBlock{Name: "memory", Role: "user", Content: strings.Repeat("user preference ", 40)})
	}

	messages, trace := ContextAssembler{}.Build(RunRequest{
		SystemPrompt:   "core system rule",
		Task:           "answer the business question with citations",
		MaxInputTokens: 32000,
		ContextBlocks:  blocks,
	})
	if trace.UsedTokens > 32000 {
		t.Fatalf("used tokens exceeded 32K budget: %+v", trace)
	}
	if trace.SavedTokens == 0 || len(trace.Compressed) == 0 || len(trace.Omitted) == 0 {
		t.Fatalf("expected compression and dedupe to improve usable space, got %+v", trace)
	}
	if trace.TokenAudit.System == 0 || trace.TokenAudit.Task == 0 {
		t.Fatalf("L1/system and task context must remain audited: %+v", trace.TokenAudit)
	}
	if messages[0].Content != "core system rule" || messages[len(messages)-1].Content != "answer the business question with citations" {
		t.Fatalf("core system prompt and task should be preserved: %+v", messages)
	}
}
