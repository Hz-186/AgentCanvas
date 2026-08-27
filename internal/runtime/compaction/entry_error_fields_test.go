package compaction

import (
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

func boolPtr(value bool) *bool { return &value }

// chatMessageFieldLock fails to compile if the llm.ChatMessage field set ever
// changes (conversion requires identical field names, types, order and tags),
// locking the no-leak contract at build time.
var chatMessageFieldLock = func() llm.ChatMessage {
	return llm.ChatMessage(struct {
		Role       string         `json:"role"`
		Content    string         `json:"content,omitempty"`
		ToolCallID string         `json:"tool_call_id,omitempty"`
		ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	}{})
}

func TestMessageSinkRowShouldNotLeakErrorFieldsThroughToChat(t *testing.T) {
	withErrorFields := []Entry{
		callEntry("c1", "lookup", `{"q":"x"}`),
		{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, Content: "boom", ToolCallID: "c1", ToolName: "lookup", IsError: boolPtr(true), ErrorCode: "invalid_arguments"},
		{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, Content: "rows", ToolCallID: "c2", ToolName: "lookup", IsError: boolPtr(false)},
	}
	legacy := []Entry{
		callEntry("c1", "lookup", `{"q":"x"}`),
		outputEntry("c1", "lookup", "boom"),
		outputEntry("c2", "lookup", "rows"),
	}

	got := ToChat(withErrorFields)
	want := ToChat(legacy)
	if len(got) != len(want) {
		t.Fatalf("error fields changed the message count: %d vs %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content || got[i].ToolCallID != want[i].ToolCallID {
			t.Fatalf("message %d leaked error fields: %+v vs %+v", i, got[i], want[i])
		}
		if len(got[i].ToolCalls) != len(want[i].ToolCalls) {
			t.Fatalf("message %d tool calls diverged: %+v vs %+v", i, got[i].ToolCalls, want[i].ToolCalls)
		}
		for callIndex := range want[i].ToolCalls {
			gotCall, wantCall := got[i].ToolCalls[callIndex], want[i].ToolCalls[callIndex]
			if gotCall.ID != wantCall.ID || gotCall.Name != wantCall.Name || string(gotCall.Arguments) != string(wantCall.Arguments) {
				t.Fatalf("message %d tool call %d diverged: %+v vs %+v", i, callIndex, gotCall, wantCall)
			}
		}
	}
	_ = chatMessageFieldLock
}
