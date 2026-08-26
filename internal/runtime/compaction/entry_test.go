package compaction

import (
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

func entryContents(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Content)
	}
	return out
}

func TestRetainKeepsUserMessagesWithinBudget(t *testing.T) {
	messages := make([]Entry, 0, 5)
	for i := 0; i < 5; i++ {
		messages = append(messages, userEntry("user message "+string(rune('a'+i))))
	}
	req := defaultReq
	req.UserBudget = 9_000
	kept := RetainUserEntries(req, messages)
	if len(kept) != 5 {
		t.Fatalf("expected 5 kept, got %d", len(kept))
	}
	for i, entry := range kept {
		want := "user message " + string(rune('a'+i))
		if entry.Content != want {
			t.Errorf("kept[%d] = %q, want %q (order broken)", i, entry.Content, want)
		}
	}
}

func TestRetainTruncatesFirstOverflowingMessage(t *testing.T) {
	small := "short one"
	medium := "this one is medium length for budget purposes"
	large := "an extremely long user message that alone would blow the entire budget many times over"
	messages := []Entry{userEntry(medium), userEntry(large), userEntry(small)}
	req := defaultReq
	req.UserBudget = len(small)/4 + len(medium)/4 + 2 // small + medium fit, large overflows
	kept := RetainUserEntries(req, messages)
	if len(kept) != 2 {
		t.Fatalf("expected truncated-large + small, got %d: %+v", len(kept), kept)
	}
	if kept[0].Content == large {
		t.Errorf("overflowing message kept untruncated")
	}
	if len(kept[0].Content) == 0 {
		t.Errorf("overflowing message truncated to empty; expected partial retention")
	}
	if kept[1].Content != small {
		t.Errorf("kept[1] = %q, want small %q", kept[1].Content, small)
	}
	if strings.Contains(strings.Join(entryContents(kept), "\n"), medium) {
		t.Errorf("earlier entry (medium) should be dropped once large overflowed")
	}
}

func TestRetainSkipsSummaryPrefixedMessages(t *testing.T) {
	messages := []Entry{
		userEntry(SummaryPrefix + "old summary"),
		userEntry("fresh question"),
	}
	kept := RetainUserEntries(defaultReq, messages)
	if len(kept) != 1 || kept[0].Content != "fresh question" {
		t.Fatalf("summary-prefixed user message leaked into retention: %+v", kept)
	}
}

func TestFromChatSplitsAssistantToolCalls(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: conversation.RoleUser, Content: "do it"},
		{
			Role:    conversation.RoleAssistant,
			Content: "I will call tools",
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`)},
				{ID: "c2", Name: "read", Arguments: json.RawMessage(`{"p":"y"}`)},
			},
		},
	}
	entries := FromChat(messages)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries (user + text + 2 calls), got %d: %+v", len(entries), entries)
	}
	if entries[1].ContentType != conversation.ContentTypeText || entries[1].Content != "I will call tools" {
		t.Errorf("text entry wrong: %+v", entries[1])
	}
	for i, want := range []string{"c1", "c2"} {
		call := entries[2+i]
		if call.ContentType != conversation.ContentTypeFunctionCall || call.ToolCallID != want {
			t.Errorf("call entry %d wrong: %+v", i, call)
		}
	}
	if entries[2].ToolName != "search" || string(entries[2].Arguments) != `{"q":"x"}` {
		t.Errorf("first call metadata wrong: %+v", entries[2])
	}
}

func TestToChatMergesConsecutiveCalls(t *testing.T) {
	entries := []Entry{
		textEntry(conversation.RoleUser, "go"),
		textEntry(conversation.RoleAssistant, "working"),
		callEntry("c1", "search", "{}"),
		callEntry("c2", "read", "{}"),
		outputEntry("c1", "search", "r1"),
		outputEntry("c2", "read", "r2"),
	}
	messages := ToChat(entries)
	if len(messages) != 4 {
		t.Fatalf("expected 4 messages (user, assistant+calls, tool, tool), got %d: %+v", len(messages), messages)
	}
	assistant := messages[1]
	if assistant.Role != conversation.RoleAssistant || assistant.Content != "working" || len(assistant.ToolCalls) != 2 {
		t.Fatalf("assistant merge wrong: %+v", assistant)
	}
	if assistant.ToolCalls[0].ID != "c1" || assistant.ToolCalls[1].ID != "c2" {
		t.Errorf("tool call order/ids wrong: %+v", assistant.ToolCalls)
	}
	for i, want := range []string{"r1", "r2"} {
		tool := messages[2+i]
		if tool.Role != conversation.RoleTool || tool.Content != want {
			t.Errorf("tool message %d wrong: %+v", i, tool)
		}
	}
	if messages[2].ToolCallID != "c1" || messages[3].ToolCallID != "c2" {
		t.Errorf("tool pairing ids wrong")
	}
}

func TestToChatSkipsReasoningAndEcho(t *testing.T) {
	entries := []Entry{
		{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeReasoning, Content: "hidden"},
		{Role: conversation.RoleUser, ContentType: conversation.ContentTypeSystemEcho, Content: "/compact"},
		userEntry("visible"),
	}
	messages := ToChat(entries)
	if len(messages) != 1 || messages[0].Content != "visible" {
		t.Fatalf("expected only visible user message, got %+v", messages)
	}
}

func TestFromMessagesParsesToolMetadata(t *testing.T) {
	messages := []conversation.Message{
		{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeFunctionCall, MetadataJSON: json.RawMessage(`{"tool_call_id":"c1","tool_name":"search","arguments":{"q":"x"}}`)},
		{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, Content: "r1", MetadataJSON: json.RawMessage(`{"tool_call_id":"c1","tool_name":"search"}`)},
	}
	entries := FromMessages(messages)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ToolCallID != "c1" || entries[0].ToolName != "search" || string(entries[0].Arguments) != `{"q":"x"}` {
		t.Errorf("function_call entry metadata wrong: %+v", entries[0])
	}
	if entries[1].ToolCallID != "c1" || entries[1].ToolName != "search" {
		t.Errorf("function_call_output entry metadata wrong: %+v", entries[1])
	}
}
