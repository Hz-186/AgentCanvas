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
