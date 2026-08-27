package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/compaction"
)

type recordingMessageWriter struct {
	rows   []*conversation.Message
	nextID int64
	err    error
}

func (w *recordingMessageWriter) Create(_ context.Context, message *conversation.Message) error {
	if w.err != nil {
		return w.err
	}
	w.nextID++
	message.ID = w.nextID
	copy := *message
	w.rows = append(w.rows, &copy)
	return nil
}

func TestMessageSinkSuppressesSubagentRuns(t *testing.T) {
	conversationID := int64(7)
	n := runtimeCore{coreRepositories: coreRepositories{MessageWriter: &recordingMessageWriter{}}}
	if sink := n.messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, DelegationDepth: 0}); sink == nil {
		t.Fatal("top-level run must get a sink")
	}
	if sink := n.messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, DelegationDepth: 1}); sink != nil {
		t.Fatal("subagent runs must not persist their transcript")
	}
}

func TestMessageSinkRequiresWriterAndConversation(t *testing.T) {
	conversationID := int64(7)
	if sink := (&runtimeCore{}).messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID}); sink != nil {
		t.Fatal("missing writer must disable the sink")
	}
	if sink := (&runtimeCore{coreRepositories: coreRepositories{MessageWriter: &recordingMessageWriter{}}}).messageSinkForRun(&RunContext{OwnerID: 1}); sink != nil {
		t.Fatal("missing conversation must disable the sink")
	}
}

func TestMessageSinkWritesTypedRowsInOrder(t *testing.T) {
	writer := &recordingMessageWriter{}
	conversationID := int64(7)
	n := runtimeCore{coreRepositories: coreRepositories{MessageWriter: writer}}
	sink := n.messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, RunID: 42, DelegationDepth: 0})
	entries := []compaction.Entry{
		{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeText, Content: "thinking out loud"},
		{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeFunctionCall, ToolCallID: "call_1", ToolName: "lookup", Arguments: json.RawMessage(`{"q":"a"}`)},
		{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, ToolCallID: "call_1", ToolName: "lookup", Content: "result"},
		{Role: conversation.RoleUser, ContentType: conversation.ContentTypeSystemEcho, Content: "/compact"},
	}
	firstID, err := sink.PersistEntries(context.Background(), entries)
	if err != nil || firstID != 1 {
		t.Fatalf("sink write failed: firstID=%d err=%v", firstID, err)
	}
	if len(writer.rows) != 3 {
		t.Fatalf("echo entries must be skipped, got %d rows: %+v", len(writer.rows), writer.rows)
	}
	first, second, third := writer.rows[0], writer.rows[1], writer.rows[2]
	if first.Role != conversation.RoleAssistant || first.ContentType != conversation.ContentTypeText || first.Content != "thinking out loud" || first.RunID == nil || *first.RunID != 42 {
		t.Fatalf("assistant text row wrong: %+v", first)
	}
	if second.ContentType != conversation.ContentTypeFunctionCall || second.MetadataJSON == nil {
		t.Fatalf("function_call row wrong: %+v", second)
	}
	callID, toolName, arguments := second.ToolMetadata()
	if callID != "call_1" || toolName != "lookup" || strings.TrimSpace(string(arguments)) != `{"q":"a"}` {
		t.Fatalf("function_call metadata wrong: %s %s %s", callID, toolName, arguments)
	}
	if third.Role != conversation.RoleTool || third.ContentType != conversation.ContentTypeFunctionCallOutput || third.Content != "result" {
		t.Fatalf("function_call_output row wrong: %+v", third)
	}
	outputCallID, outputToolName, _ := third.ToolMetadata()
	if outputCallID != "call_1" || outputToolName != "lookup" {
		t.Fatalf("output metadata wrong: %s %s", outputCallID, outputToolName)
	}
}

func TestMessageSinkSkipsMalformedToolEntries(t *testing.T) {
	writer := &recordingMessageWriter{}
	conversationID := int64(7)
	n := runtimeCore{coreRepositories: coreRepositories{MessageWriter: writer}}
	sink := n.messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, RunID: 42, DelegationDepth: 0})
	entries := []compaction.Entry{
		{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeFunctionCall, ToolCallID: "call_1", ToolName: "", Arguments: json.RawMessage(`{}`)},
		{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, ToolCallID: "", Content: "orphan"},
	}
	firstID, err := sink.PersistEntries(context.Background(), entries)
	if err != nil || firstID != 0 {
		t.Fatalf("malformed entries must be skipped silently: firstID=%d err=%v", firstID, err)
	}
	if len(writer.rows) != 0 {
		t.Fatalf("no rows expected: %+v", writer.rows)
	}
}

func TestMessageSinkCarriesStableTranscriptIdentity(t *testing.T) {
	writer := &recordingMessageWriter{}
	conversationID := int64(7)
	sink := (&runtimeCore{coreRepositories: coreRepositories{MessageWriter: writer}}).messageSinkForRun(&RunContext{OwnerID: 1, ConversationID: &conversationID, RunID: 42})
	entry := compaction.FromChatAt([]llm.ChatMessage{{Role: conversation.RoleAssistant, Content: "done"}}, 3)[0]
	if _, err := sink.PersistEntries(context.Background(), []compaction.Entry{entry}); err != nil {
		t.Fatal(err)
	}
	if len(writer.rows) != 1 || writer.rows[0].TranscriptEntryID == nil || *writer.rows[0].TranscriptEntryID != "3:0" {
		t.Fatalf("row must retain stable transcript identity: %+v", writer.rows)
	}
}
