package agent

import (
	"context"
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

// CitationBoundaryTest#shouldPersistStrippedRowKeepRawAnswerForAccounting
// proves the runner persistence boundary: the transcript row written through
// the message sink carries the stripped text, the emitted/stored final answer
// step is stripped, but result.FinalAnswer keeps the RAW block so the
// finalizer in agentruntime can still parse it for owner-validated usage
// accounting.
func TestCitationBoundaryShouldPersistStrippedRowKeepRawAnswerForAccounting(t *testing.T) {
	raw := "Answer body.\n\n" +
		`<oai-mem-citation memory_id="101">adopted</oai-mem-citation>` + "\n"
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: raw}},
	}}
	sink := &recordingMessageSink{}
	result, err := NewRunner(client).Run(context.Background(), RunRequest{
		Provider:    llm.ChatProviderConfig{ProviderType: "openai_compatible"},
		Model:       "m",
		Task:        "task",
		MessageSink: sink,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	// Accounting still sees the raw block.
	if result.FinalAnswer != raw {
		t.Fatalf("FinalAnswer must stay raw for citation accounting, got %q", result.FinalAnswer)
	}
	// The persisted transcript row carries the stripped text.
	entries := sink.entries()
	if len(entries) != 1 {
		t.Fatalf("sink must receive one assistant text row, got %d: %+v", len(entries), entries)
	}
	if entries[0].ContentType != conversation.ContentTypeText || entries[0].Role != conversation.RoleAssistant {
		t.Fatalf("persisted row must be assistant text: %+v", entries[0])
	}
	if entries[0].Content != "Answer body." {
		t.Fatalf("persisted row content = %q, want citation block stripped", entries[0].Content)
	}
	if strings.Contains(entries[0].Content, "oai-mem-citation") {
		t.Fatalf("persisted row still contains citation markup: %q", entries[0].Content)
	}
	// The emitted/stored final answer step carries the stripped text too.
	var finalStep *RunStep
	for i := range result.Steps {
		if result.Steps[i].Type == StepTypeFinalAnswer {
			finalStep = &result.Steps[i]
		}
	}
	if finalStep == nil {
		t.Fatalf("missing final answer step: %+v", result.Steps)
	}
	if finalStep.Content != "Answer body." {
		t.Fatalf("final answer step content = %q, want stripped", finalStep.Content)
	}
}

// CitationBoundaryTest#shouldLeaveCitationFreeAnswersUntouched
func TestCitationBoundaryShouldLeaveCitationFreeAnswersUntouched(t *testing.T) {
	client := &fakeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "plain answer"}},
	}}
	sink := &recordingMessageSink{}
	result, err := NewRunner(client).Run(context.Background(), RunRequest{
		Provider:    llm.ChatProviderConfig{ProviderType: "openai_compatible"},
		Model:       "m",
		Task:        "task",
		MessageSink: sink,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.FinalAnswer != "plain answer" {
		t.Fatalf("FinalAnswer = %q, want unchanged", result.FinalAnswer)
	}
	entries := sink.entries()
	if len(entries) != 1 || entries[0].Content != "plain answer" {
		t.Fatalf("persisted row = %+v, want unchanged plain answer", entries)
	}
}
