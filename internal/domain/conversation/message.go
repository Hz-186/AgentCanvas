package conversation

import "time"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"

	ContentTypeText = "text"
)

type Message struct {
	ID             int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID        int64     `json:"owner_id" gorm:"column:owner_id"`
	ConversationID int64     `json:"conversation_id" gorm:"column:conversation_id"`
	Role           string    `json:"role" gorm:"column:role"`
	Content        string    `json:"content" gorm:"column:content"`
	ContentType    string    `json:"content_type" gorm:"column:content_type"`
	RunID          *int64    `json:"run_id,omitempty" gorm:"column:run_id"`
	TokenCount     int       `json:"token_count" gorm:"column:token_count"`
	MetadataJSON   string    `json:"metadata_json,omitempty" gorm:"column:metadata_json"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
}

func (Message) TableName() string { return "messages" }
