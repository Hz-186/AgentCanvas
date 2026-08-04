package conversation

import (
	"context"
	"time"
)

type Repository interface {
	Create(ctx context.Context, item *Conversation) error
	ListByOwner(ctx context.Context, ownerID int64) ([]Conversation, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Conversation, error)
	Update(ctx context.Context, item *Conversation) error
	UpdateLastMessageAt(ctx context.Context, ownerID, id int64) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type AgentRepository interface {
	Repository
	ListByAgent(ctx context.Context, ownerID, agentID int64) ([]Conversation, error)
	UpdateAgentMode(ctx context.Context, ownerID, id int64, mode string) error
}

type MessageRepository interface {
	Create(ctx context.Context, message *Message) error
	ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]Message, error)
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]Message, error)
	ListByRun(ctx context.Context, ownerID, runID int64) ([]Message, error)
	CreateReferences(ctx context.Context, refs []MessageReference) error
	ListReferencesByMessage(ctx context.Context, ownerID, messageID int64) ([]MessageReference, error)
}

type MessageSearchRequest struct {
	OwnerID        int64
	AgentID        int64
	ConversationID *int64
	Query          string
	From           *time.Time
	To             *time.Time
	Limit          int
}

type MessageSearchResult struct {
	MessageID      int64     `json:"message_id"`
	AgentID        int64     `json:"agent_id"`
	ConversationID int64     `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	Score          float64   `json:"score"`
	CreatedAt      time.Time `json:"created_at"`
}

type MessageSearchIndex interface {
	EnsureIndex(context.Context) error
	IndexMessage(context.Context, int64, int64, *Message) error
	SearchMessages(context.Context, MessageSearchRequest) ([]MessageSearchResult, error)
	DeleteConversation(context.Context, int64, int64, int64) error
}
