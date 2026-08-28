package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/compaction"
)

// enrichmentTranscript covers one failed, one successful and one step-less
// tool output, all from the same tool name so only exact ToolCallID matching
// can enrich correctly.
func enrichmentTranscript() []llm.ChatMessage {
	return []llm.ChatMessage{
		{
			Role: conversation.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_failed", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
				{ID: "call_ok", Name: "lookup", Arguments: json.RawMessage(`{"q":"y"}`)},
				{ID: "call_orphan", Name: "lookup", Arguments: json.RawMessage(`{"q":"z"}`)},
			},
		},
		{Role: conversation.RoleTool, ToolCallID: "call_failed", Content: "invalid arguments supplied"},
		{Role: conversation.RoleTool, ToolCallID: "call_ok", Content: "rows"},
		{Role: conversation.RoleTool, ToolCallID: "call_orphan", Content: "orphan output"},
	}
}

func enrichmentSteps() []agent.RunStep {
	return []agent.RunStep{
		// The tool_call step shares the call ID with the tool_result step;
		// enrichment must read only tool_result steps.
		{Type: agent.StepTypeToolCall, ToolCallID: "call_failed", ToolName: "lookup"},
		{Type: agent.StepTypeToolResult, ToolCallID: "call_failed", ToolName: "lookup", IsError: true, ErrorCode: "invalid_arguments"},
		{Type: agent.StepTypeToolResult, ToolCallID: "call_ok", ToolName: "lookup"},
	}
}

func outputEntryByCallID(t *testing.T, entries []compaction.Entry, toolCallID string) compaction.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.ContentType == conversation.ContentTypeFunctionCallOutput && entry.ToolCallID == toolCallID {
			return entry
		}
	}
	t.Fatalf("no function_call_output entry for %s in %+v", toolCallID, entries)
	return compaction.Entry{}
}

func TestTranscriptErrorEnrichment(t *testing.T) {
	t.Run("shouldEnrichOutputEntriesFromStepLookup", func(t *testing.T) {
		entries := compaction.FromChatAt(enrichmentTranscript(), 0)
		agent.EnrichTranscriptEntries(entries, enrichmentSteps())

		failed := outputEntryByCallID(t, entries, "call_failed")
		if failed.IsError == nil || !*failed.IsError || failed.ErrorCode != "invalid_arguments" {
			t.Fatalf("failed output entry must carry the step error state: %+v", failed)
		}
		if failed.Content != "invalid arguments supplied" {
			t.Fatalf("enrichment must not touch content: %+v", failed)
		}
		ok := outputEntryByCallID(t, entries, "call_ok")
		if ok.IsError == nil || *ok.IsError || ok.ErrorCode != "" {
			t.Fatalf("successful output entry must carry explicit success state: %+v", ok)
		}
		for _, entry := range entries {
			if entry.ContentType == conversation.ContentTypeFunctionCall && entry.IsError != nil {
				t.Fatalf("function_call entries must never be enriched: %+v", entry)
			}
		}
	})

	t.Run("shouldLeaveUnknownWhenStepMissing", func(t *testing.T) {
		entries := compaction.FromChatAt(enrichmentTranscript(), 0)
		agent.EnrichTranscriptEntries(entries, enrichmentSteps())

		orphan := outputEntryByCallID(t, entries, "call_orphan")
		if orphan.IsError != nil || orphan.ErrorCode != "" {
			t.Fatalf("entries without a matching step must keep no error fields: %+v", orphan)
		}
	})

	t.Run("shouldBeDeterministicAcrossReplays", func(t *testing.T) {
		conversationID := int64(7)
		var rows [][]*conversation.Message
		for replay := 0; replay < 2; replay++ {
			writer := &recordingMessageWriter{}
			sink := (&runtimeCore{coreRepositories: coreRepositories{MessageWriter: writer}}).messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, RunID: 42})
			entries := compaction.FromChatAt(enrichmentTranscript(), 0)
			agent.EnrichTranscriptEntries(entries, enrichmentSteps())
			if _, err := sink.PersistEntries(context.Background(), entries); err != nil {
				t.Fatalf("replay %d persist failed: %v", replay, err)
			}
			rows = append(rows, writer.rows)
		}
		if len(rows[0]) != len(rows[1]) || len(rows[0]) == 0 {
			t.Fatalf("replays produced different row counts: %d vs %d", len(rows[0]), len(rows[1]))
		}
		for i := range rows[0] {
			first, second := rows[0][i], rows[1][i]
			if !bytes.Equal(first.MetadataJSON, second.MetadataJSON) {
				t.Fatalf("replay metadata_json diverges at row %d: %s vs %s", i, first.MetadataJSON, second.MetadataJSON)
			}
			if first.Content != second.Content || first.ContentType != second.ContentType || first.TranscriptEntryID == nil || *first.TranscriptEntryID != *second.TranscriptEntryID {
				t.Fatalf("replay rows diverge at %d: %+v vs %+v", i, first, second)
			}
		}
		// The byte comparison above is only meaningful once enriched rows
		// actually carry the error keys.
		enriched := false
		for _, row := range rows[0] {
			if row.ContentType == conversation.ContentTypeFunctionCallOutput && bytes.Contains(row.MetadataJSON, []byte(`"is_error"`)) {
				enriched = true
			}
		}
		if !enriched {
			t.Fatal("replayed failed output rows must carry is_error metadata")
		}
	})
}
