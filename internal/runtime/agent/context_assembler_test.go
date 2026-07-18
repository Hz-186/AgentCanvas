package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/tokencounter"
	"agentcanvas/internal/runtime/harness/rules"
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

func TestContextAssemblerDoesNotSilentlyTruncateCoreRules(t *testing.T) {
	messages, trace := ContextAssembler{MaxInputTokens: 4}.Build(RunRequest{
		Task: "task",
		ContextBlocks: []ContextBlock{{
			Name:    "rules_mandatory:core",
			Role:    "system",
			Content: strings.Repeat("core ", 20),
			Pinned:  true,
		}},
	})
	if !trace.CoreOverflow {
		t.Fatalf("expected core overflow to be visible, got %+v", trace)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "core core") {
			t.Fatalf("core rule must not be silently truncated or included: %+v", messages)
		}
	}
}

func TestContextAssemblerAccumulatesMandatoryRulesWithModelTokenizer(t *testing.T) {
	first := "Never disclose credentials or private keys."
	second := "Require explicit approval before destructive operations."
	_, trace := ContextAssembler{MaxInputTokens: 1000}.Build(RunRequest{
		Provider: llm.ChatProviderConfig{ProviderType: "openai"},
		Model:    "gpt-4",
		Task:     "answer",
		ContextBlocks: []ContextBlock{
			{Name: "rules_mandatory:safety", Role: "system", Content: first, Pinned: true},
			{Name: "rules_mandatory:approval", Role: "system", Content: second, Pinned: true},
		},
	})
	expected := tokencounter.Count("openai", "gpt-4", first).Tokens + tokencounter.Count("openai", "gpt-4", second).Tokens
	if trace.MandatoryTokens != expected || trace.TokenAudit.RulesMandatory != expected {
		t.Fatalf("mandatory token accounting must use model tokenizer: expected=%d trace=%+v", expected, trace)
	}
}

func TestRunnerReturnsTypedMandatoryBudgetErrorBeforeCallingModel(t *testing.T) {
	client := &fakeToolClient{}
	runner := NewRunner(client)
	_, err := runner.Run(context.Background(), RunRequest{
		Model: "test", Task: "task", MaxInputTokens: 4,
		ContextBlocks: []ContextBlock{{Name: "rules_mandatory:tenant.mandatory", Role: "system", Content: strings.Repeat("mandatory ", 20), Pinned: true}},
	})
	if !errors.Is(err, ErrMandatoryRuleBudgetExceeded) {
		t.Fatalf("expected typed mandatory overflow, got %v", err)
	}
	if !strings.Contains(err.Error(), "deficit_tokens=") {
		t.Fatalf("mandatory overflow must report token deficit, got %v", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("model must not be called on mandatory overflow, requests=%d", len(client.requests))
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
			{Name: "rules_mandatory:safety", Role: "system", Content: "never fabricate outputs"},
			{Name: "conversation", Role: "user", Content: "previous turn"},
			{Name: "memory", Role: "user", Content: "remembered preference"},
			{Name: "retrieval:kb", Role: "user", Content: "knowledge chunk content"},
			{Name: "rules_optional:rag", Role: "system", Content: "cite retrieved chunks"},
			{Name: "rules_optional:compression", Role: "system", Content: "summarize repeated context when safe"},
		},
	})
	if trace.TokenAudit.Total != trace.EstimatedTokens {
		t.Fatalf("token audit total = %d, estimated = %d", trace.TokenAudit.Total, trace.EstimatedTokens)
	}
	if trace.TokenAudit.System == 0 || trace.TokenAudit.History == 0 || trace.TokenAudit.Memory == 0 || trace.TokenAudit.Retrieval == 0 || trace.TokenAudit.Task == 0 || trace.TokenAudit.RulesMandatory == 0 || trace.TokenAudit.RulesOptional == 0 {
		t.Fatalf("missing token audit categories: %+v", trace.TokenAudit)
	}
}

func TestContextAssemblerDropsReflectionBeforeMandatoryRules(t *testing.T) {
	_, trace := ContextAssembler{}.Build(RunRequest{
		Task:           "task",
		Provider:       llm.ChatProviderConfig{ProviderType: "openai"},
		Model:          "gpt-4",
		MaxInputTokens: 12,
		ContextBlocks: []ContextBlock{
			{Name: "reflection_memory", Role: "system", Content: strings.Repeat("lesson ", 30)},
			{Name: "rules_mandatory:safety", Role: "system", Content: "never bypass safety", Pinned: true},
			{Name: "rules_mandatory:tenant", Role: "system", Content: "respect tenant policy", Pinned: true},
		},
	})
	if trace.CoreOverflow {
		t.Fatalf("reflection memory must not consume the mandatory rule budget: %+v", trace)
	}
	if trace.TokenAudit.RulesMandatory == 0 {
		t.Fatalf("mandatory rules were not retained: %+v", trace.TokenAudit)
	}
	if trace.TokenAudit.ReflectionMemory != 0 {
		t.Fatalf("reflection should be omitted under pressure: %+v", trace.TokenAudit)
	}
}

func TestContextAssemblerSortsCoreMemoryBeforeHistoryAndRetrieval(t *testing.T) {
	messages, _ := ContextAssembler{MaxChars: 5000}.Build(RunRequest{
		Task: "task",
		ContextBlocks: []ContextBlock{
			{Name: "retrieval:kb", Role: "system", Content: "retrieval"},
			{Name: "conversation", Role: "user", Content: "history"},
			{Name: "core_memory", Role: "system", Content: "core"},
		},
	})
	if len(messages) < 4 {
		t.Fatalf("unexpected messages: %+v", messages)
	}
	if messages[0].Content != "core" || messages[1].Content != "history" || messages[2].Content != "retrieval" {
		t.Fatalf("unexpected sorted order: %+v", messages)
	}
}

func TestContextAssemblerCarriesRuleTrace(t *testing.T) {
	_, trace := ContextAssembler{MaxChars: 5000}.Build(RunRequest{
		Task: "answer the task",
		RuleTrace: rules.Trace{
			Loaded:            []string{"core.task.completion"},
			SelectionStrategy: "deterministic_activation_budget:v1",
		},
	})
	if trace.RuleTrace.SelectionStrategy != "deterministic_activation_budget:v1" || len(trace.RuleTrace.Loaded) != 1 {
		t.Fatalf("expected rule trace to be preserved, got %+v", trace.RuleTrace)
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

func TestContextAssemblerRetainsNovelOldHistoryWithinCompression(t *testing.T) {
	blocks := []ContextBlock{
		{Name: "conversation", Role: "user", Content: strings.Repeat("repeat repeat repeat ", 80)},
		{Name: "conversation", Role: "assistant", Content: "rare regression keyword: webhook-signature-drift"},
		{Name: "conversation", Role: "user", Content: "turn 3 recent"},
		{Name: "conversation", Role: "assistant", Content: "turn 4 recent"},
		{Name: "conversation", Role: "user", Content: "turn 5 recent"},
		{Name: "conversation", Role: "assistant", Content: "turn 6 recent"},
	}

	messages, trace := ContextAssembler{MaxInputTokens: 2000}.Build(RunRequest{Task: "task", ContextBlocks: blocks})
	foundNovelHistory := false
	foundSummary := false
	for _, message := range messages {
		if strings.Contains(message.Content, "webhook-signature-drift") && !strings.Contains(message.Content, "Earlier conversation summary") {
			foundNovelHistory = true
		}
		if strings.Contains(message.Content, "Earlier conversation summary") {
			foundSummary = true
		}
	}
	if !foundNovelHistory {
		t.Fatalf("expected novel old history to be retained as original content, trace=%+v messages=%+v", trace, messages)
	}
	if !foundSummary {
		t.Fatalf("expected repetitive old history to be summarized, trace=%+v messages=%+v", trace, messages)
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
