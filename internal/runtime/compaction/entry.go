// Package compaction holds the single context-compaction algorithm shared by
// cross-turn (conversationcontext) and mid-run (agent) triggers, mirroring the
// Codex single-algorithm/multi-trigger design.
package compaction

import (
	"encoding/json"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

// Entry is the unified history item model. A row from the messages table and
// an in-memory ChatMessage both convert to it, so every trigger feeds the
// same algorithm the same shape.
type Entry struct {
	MessageID   int64           // messages-table row ID; 0 for in-memory history
	Role        string          // user/assistant/tool/developer/system
	ContentType string          // text/function_call/function_call_output/reasoning/system_echo
	Content     string          // text content or tool output content
	ToolCallID  string          // pairs function_call with function_call_output
	ToolName    string
	Arguments   json.RawMessage // function_call arguments
}

// FromMessages converts persisted conversation rows to entries. MetadataJSON
// parsing failures degrade to plain text entries (never error).
func FromMessages(messages []conversation.Message) []Entry {
	entries := make([]Entry, 0, len(messages))
	for i := range messages {
		msg := messages[i]
		entry := Entry{MessageID: msg.ID, Role: msg.Role, ContentType: msg.ContentType, Content: msg.Content}
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
	entries := make([]Entry, 0, len(messages))
	for _, msg := range messages {
		switch {
		case len(msg.ToolCalls) > 0:
			if msg.Content != "" {
				entries = append(entries, Entry{Role: conversation.RoleAssistant, ContentType: conversation.ContentTypeText, Content: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				entries = append(entries, Entry{
					Role:        conversation.RoleAssistant,
					ContentType: conversation.ContentTypeFunctionCall,
					ToolCallID:  call.ID,
					ToolName:    call.Name,
					Arguments:   call.Arguments,
				})
			}
		case msg.Role == conversation.RoleTool:
			entries = append(entries, Entry{Role: conversation.RoleTool, ContentType: conversation.ContentTypeFunctionCallOutput, Content: msg.Content, ToolCallID: msg.ToolCallID})
		default:
			entries = append(entries, Entry{Role: msg.Role, ContentType: conversation.ContentTypeText, Content: msg.Content})
		}
	}
	return entries
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
