// Package compaction holds the single context-compaction algorithm shared by
// cross-turn (conversationcontext) and mid-run (agent) triggers.
package compaction

import (
	"encoding/json"
	"fmt"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

// Entry is the unified history item model. A row from the messages table and
// an in-memory ChatMessage both convert to it, so every trigger feeds the
// same algorithm the same shape.
type Entry struct {
	MessageID int64 // messages-table row ID; 0 for in-memory history
	// TranscriptIndex and SubEntryIndex identify the source ChatMessage and its
	// expanded entry within that message. They are assigned by FromChatAt and
	// allow a retried sink batch to address the same durable row exactly once.
	TranscriptIndex int
	SubEntryIndex   int
	// TranscriptEntryID is populated only for entries generated from a runner
	// transcript. Empty values keep non-runtime callers on the legacy path.
	TranscriptEntryID string
	Role              string // user/assistant/tool/developer/system
	ContentType       string // text/function_call/function_call_output/reasoning/system_echo
	Content           string // text content or tool output content
	ToolCallID        string // pairs function_call with function_call_output
	ToolName          string
	Arguments         json.RawMessage // function_call arguments
}

// FromMessages converts persisted conversation rows to entries. MetadataJSON
// parsing failures degrade to plain text entries (never error).
func FromMessages(messages []conversation.Message) []Entry {
	entries := make([]Entry, 0, len(messages))
	for i := range messages {
		msg := messages[i]
		entry := Entry{MessageID: msg.ID, Role: msg.Role, ContentType: msg.ContentType, Content: msg.Content}
		if msg.TranscriptEntryID != nil {
			entry.TranscriptEntryID = *msg.TranscriptEntryID
		}
		if entry.ContentType == "" {
			entry.ContentType = conversation.ContentTypeText
		}
		switch entry.ContentType {
		case conversation.ContentTypeFunctionCall, conversation.ContentTypeFunctionCallOutput:
			toolCallID, toolName, arguments := msg.ToolMetadata()
			entry.ToolCallID, entry.ToolName, entry.Arguments = toolCallID, toolName, arguments
		}
		entries = append(entries, entry)
	}
	return entries
}

// FromChat converts in-memory transcript messages to entries. An assistant
// message with N tool calls splits into an optional text entry followed by N
// function_call entries; role=tool messages become function_call_output.
func FromChat(messages []llm.ChatMessage) []Entry {
	return FromChatAt(messages, 0)
}

// FromChatAt is FromChat with a stable transcript offset. Assistant messages
// may expand into several database entries, so each expanded row receives the
// source message index plus a per-message sub-index.
func FromChatAt(messages []llm.ChatMessage, startIndex int) []Entry {
	entries := make([]Entry, 0, len(messages))
	for messageIndex, msg := range messages {
		subIndex := 0
		switch {
		case len(msg.ToolCalls) > 0:
			if msg.Content != "" {
				entries = append(entries, newTranscriptEntry(startIndex+messageIndex, subIndex, conversation.RoleAssistant, conversation.ContentTypeText, msg.Content))
				subIndex++
			}
			for _, call := range msg.ToolCalls {
				entry := newTranscriptEntry(startIndex+messageIndex, subIndex, conversation.RoleAssistant, conversation.ContentTypeFunctionCall, "")
				entry.ToolCallID = call.ID
				entry.ToolName = call.Name
				entry.Arguments = call.Arguments
				entries = append(entries, entry)
				subIndex++
			}
		case msg.Role == conversation.RoleTool:
			entry := newTranscriptEntry(startIndex+messageIndex, subIndex, conversation.RoleTool, conversation.ContentTypeFunctionCallOutput, msg.Content)
			entry.ToolCallID = msg.ToolCallID
			entries = append(entries, entry)
		default:
			entries = append(entries, newTranscriptEntry(startIndex+messageIndex, subIndex, msg.Role, conversation.ContentTypeText, msg.Content))
		}
	}
	return entries
}

func newTranscriptEntry(transcriptIndex, subEntryIndex int, role, contentType, content string) Entry {
	return Entry{
		TranscriptIndex:   transcriptIndex,
		SubEntryIndex:     subEntryIndex,
		TranscriptEntryID: fmt.Sprintf("%d:%d", transcriptIndex, subEntryIndex),
		Role:              role,
		ContentType:       contentType,
		Content:           content,
	}
}

// ToChat converts entries back to request messages. An adjacent text entry
// followed by consecutive function_call entries merge into one assistant
// message (Content + ToolCalls); each function_call_output becomes its own
// role=tool message so providers see legal call/output pairing.
func ToChat(entries []Entry) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, len(entries))
	pending := -1 // index into messages of the assistant message currently absorbing function_call entries
	for _, entry := range entries {
		switch entry.ContentType {
		case conversation.ContentTypeText:
			pending = -1
			messages = append(messages, llm.ChatMessage{Role: entry.Role, Content: entry.Content})
			if entry.Role == conversation.RoleAssistant {
				pending = len(messages) - 1
			}
		case conversation.ContentTypeFunctionCall:
			if pending < 0 {
				messages = append(messages, llm.ChatMessage{Role: conversation.RoleAssistant})
				pending = len(messages) - 1
			}
			messages[pending].ToolCalls = append(messages[pending].ToolCalls, llm.ToolCall{ID: entry.ToolCallID, Name: entry.ToolName, Arguments: entry.Arguments})
		case conversation.ContentTypeFunctionCallOutput:
			pending = -1
			messages = append(messages, llm.ChatMessage{Role: conversation.RoleTool, Content: entry.Content, ToolCallID: entry.ToolCallID})
		default:
			// reasoning/system_echo never replay to the model.
		}
	}
	return messages
}
