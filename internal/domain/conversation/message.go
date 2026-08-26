package conversation

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/domain"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
	RoleDeveloper = "developer"
)

const (
	ContentTypeText               = "text"
	ContentTypeFunctionCall       = "function_call"
	ContentTypeFunctionCallOutput = "function_call_output"
	ContentTypeReasoning          = "reasoning"
	ContentTypeSystemEcho         = "system_echo"
)

type Message struct {
	domain.ImmutableModel
	ConversationID int64           `json:"conversation_id" gorm:"column:conversation_id"`
	Role           string          `json:"role" gorm:"column:role"`
	Content        string          `json:"content" gorm:"column:content"`
	RunID          *int64          `json:"run_id,omitempty" gorm:"column:run_id"`
	TokenCount     int             `json:"token_count" gorm:"column:token_count"`
	ArchivedAt     *time.Time      `json:"archived_at,omitempty" gorm:"column:archived_at"`
	ContentType    string          `json:"content_type" gorm:"column:content_type"`
	MetadataJSON   json.RawMessage `json:"metadata_json,omitempty" gorm:"column:metadata_json"`
}

func (Message) TableName() string { return "messages" }

// ToolMetadata returns the tool metadata stored in MetadataJSON using the
// tool_call_id/tool_name/arguments keys. Invalid JSON degrades to zero values
// instead of an error so callers can treat the message as plain text.
func (m Message) ToolMetadata() (toolCallID, toolName string, arguments json.RawMessage) {
	if len(m.MetadataJSON) == 0 {
		return "", "", nil
	}
	var metadata struct {
		ToolCallID string          `json:"tool_call_id"`
		ToolName   string          `json:"tool_name"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(m.MetadataJSON, &metadata); err != nil {
		return "", "", nil
	}
	return metadata.ToolCallID, metadata.ToolName, metadata.Arguments
}
