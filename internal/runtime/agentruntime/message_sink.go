package agentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/compaction"
)

// conversationMessageSink persists transcript entries as typed message rows
// (design §3): assistant text and function_call entries become one row each,
// function_call_output becomes a role=tool row. system_echo/reasoning entries
// never reach the database. Rows are not search-indexed (design §6): the
// session index keeps text-only visibility.
type conversationMessageSink struct {
	writer         MessageWriter
	ownerID        int64
	conversationID int64
	runID          int64
}

// messageSinkForRun returns the realtime sink for a run, or nil when realtime
// writes must be suppressed: subagent runs (DelegationDepth>0) never persist
// their transcript to the shared conversation (design §4.1/§5).
func (n *runtimeCore) messageSinkForRun(rc *RunContext) agent.MessageSink {
	if n.MessageWriter == nil || rc == nil || rc.ConversationID == nil || *rc.ConversationID <= 0 {
		return nil
	}
	if rc.DelegationDepth > 0 {
		return nil
	}
	return &conversationMessageSink{
		writer:         n.MessageWriter,
		ownerID:        rc.OwnerID,
		conversationID: *rc.ConversationID,
		runID:          rc.RunID,
	}
}

// PersistEntries writes entries in order and returns the first row ID.
// Assistant text is written first so text rows always precede the
// function_call rows of the same model response (design §3 ordering).
func (s *conversationMessageSink) PersistEntries(ctx context.Context, entries []compaction.Entry) (int64, error) {
	var firstID int64
	for _, entry := range entries {
		row := s.rowFor(entry)
		if row == nil {
			continue
		}
		if err := s.writer.Create(ctx, row); err != nil {
			return firstID, err
		}
		if firstID == 0 {
			firstID = row.ID
		}
	}
	return firstID, nil
}

func (s *conversationMessageSink) rowFor(entry compaction.Entry) *conversation.Message {
	row := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{OwnerID: s.ownerID},
		ConversationID: s.conversationID,
		Role:           entry.Role,
		ContentType:    entry.ContentType,
		RunID:          &s.runID,
	}
	if entry.TranscriptEntryID != "" {
		id := entry.TranscriptEntryID
		row.TranscriptEntryID = &id
	}
	switch entry.ContentType {
	case conversation.ContentTypeFunctionCall:
		if strings.TrimSpace(entry.ToolName) == "" {
			return nil
		}
		arguments := entry.Arguments
		if len(strings.TrimSpace(string(arguments))) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		metadata, err := json.Marshal(map[string]any{
			"tool_call_id": entry.ToolCallID,
			"tool_name":    entry.ToolName,
			"arguments":    arguments,
		})
		if err != nil {
			return nil
		}
		row.Content = ""
		row.MetadataJSON = metadata
	case conversation.ContentTypeFunctionCallOutput:
		if strings.TrimSpace(entry.ToolCallID) == "" {
			return nil
		}
		metadata, err := json.Marshal(map[string]any{
			"tool_call_id": entry.ToolCallID,
			"tool_name":    entry.ToolName,
		})
		if err != nil {
			return nil
		}
		row.Content = entry.Content
		row.MetadataJSON = metadata
	case conversation.ContentTypeText:
		row.Content = entry.Content
	default:
		// reasoning/system_echo never persist through the sink.
		return nil
	}
	return row
}
