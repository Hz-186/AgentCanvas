package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/runtime/compaction"
)

func persistRowsForTest(t *testing.T, entries []compaction.Entry) []*conversation.Message {
	t.Helper()
	writer := &recordingMessageWriter{}
	conversationID := int64(7)
	sink := (&runtimeCore{coreRepositories: coreRepositories{MessageWriter: writer}}).messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, RunID: 42})
	if _, err := sink.PersistEntries(context.Background(), entries); err != nil {
		t.Fatalf("persist failed: %v", err)
	}
	return writer.rows
}

func metadataMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("metadata_json is not an object: %s", raw)
	}
	return m
}

func assertMetadataKeys(t *testing.T, m map[string]any, want ...string) {
	t.Helper()
	if len(m) != len(want) {
		t.Fatalf("metadata key set = %v, want exactly %v", m, want)
	}
	for _, key := range want {
		if _, ok := m[key]; !ok {
			t.Fatalf("metadata missing key %q: %v", key, m)
		}
	}
}

func TestMessageSinkRow(t *testing.T) {
	failed := true
	success := false

	t.Run("shouldWriteErrorMetadataForFailedOutput", func(t *testing.T) {
		rows := persistRowsForTest(t, []compaction.Entry{{
			Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput,
			ToolCallID: "call_1", ToolName: "lookup", Content: "boom output",
			IsError: &failed, ErrorCode: "boom",
		}})
		if len(rows) != 1 {
			t.Fatalf("expected exactly one Create call, got %d rows", len(rows))
		}
		row := rows[0]
		if row.Content != "boom output" {
			t.Fatalf("row content must stay unchanged: %+v", row)
		}
		meta := metadataMap(t, row.MetadataJSON)
		assertMetadataKeys(t, meta, "tool_call_id", "tool_name", "is_error", "error_code")
		if meta["is_error"] != true || meta["error_code"] != "boom" || meta["tool_call_id"] != "call_1" || meta["tool_name"] != "lookup" {
			t.Fatalf("metadata values wrong: %v", meta)
		}
	})

	t.Run("shouldWriteExplicitSuccessFlag", func(t *testing.T) {
		rows := persistRowsForTest(t, []compaction.Entry{{
			Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput,
			ToolCallID: "call_1", ToolName: "lookup", Content: "rows",
			IsError: &success,
		}})
		if len(rows) != 1 {
			t.Fatalf("expected exactly one Create call, got %d rows", len(rows))
		}
		meta := metadataMap(t, rows[0].MetadataJSON)
		if _, known := meta["is_error"]; !known {
			t.Fatalf("enriched success rows must carry an explicit is_error flag so legacy unknown rows stay distinguishable: %v", meta)
		}
		if meta["is_error"] != false {
			t.Fatalf("is_error must be false for enriched success rows: %v", meta)
		}
	})

	t.Run("shouldWriteEmptyErrorCodeWhenStepLacksCode", func(t *testing.T) {
		rows := persistRowsForTest(t, []compaction.Entry{{
			Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput,
			ToolCallID: "call_1", ToolName: "lookup", Content: "execution failed",
			IsError: &failed, ErrorCode: "",
		}})
		if len(rows) != 1 {
			t.Fatalf("expected exactly one Create call, got %d rows", len(rows))
		}
		meta := metadataMap(t, rows[0].MetadataJSON)
		assertMetadataKeys(t, meta, "tool_call_id", "tool_name", "is_error", "error_code")
		if code, present := meta["error_code"]; !present || code != "" {
			t.Fatalf("error_code must be a deterministic present empty string: %v", meta)
		}
		if meta["is_error"] != true {
			t.Fatalf("is_error must stay true: %v", meta)
		}
	})

	t.Run("shouldNotAddErrorKeysToFunctionCallRows", func(t *testing.T) {
		rows := persistRowsForTest(t, []compaction.Entry{{
			Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeFunctionCall,
			ToolCallID: "call_1", ToolName: "lookup", Arguments: json.RawMessage(`{"q":"x"}`),
		}})
		if len(rows) != 1 {
			t.Fatalf("expected exactly one Create call, got %d rows", len(rows))
		}
		meta := metadataMap(t, rows[0].MetadataJSON)
		assertMetadataKeys(t, meta, "tool_call_id", "tool_name", "arguments")
	})

	t.Run("shouldKeepReasoningAndSystemEchoDropped", func(t *testing.T) {
		rows := persistRowsForTest(t, []compaction.Entry{
			{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeReasoning, Content: "thinking", IsError: &failed, ErrorCode: "x"},
			{Role: conversation.RoleUser, ContentType: conversation.ContentTypeSystemEcho, Content: "/compact", IsError: &success},
		})
		if len(rows) != 0 {
			t.Fatalf("reasoning/system_echo entries must never reach writer.Create, got %d rows: %+v", len(rows), rows)
		}
	})
}
