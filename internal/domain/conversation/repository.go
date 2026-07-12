package conversation

import "context"

type Repository interface {
	Create(ctx context.Context, item *Conversation) error
	ListByOwner(ctx context.Context, ownerID int64) ([]Conversation, error)
	ListByDialog(ctx context.Context, ownerID, dialogID int64) ([]Conversation, error)
	ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]Conversation, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Conversation, error)
	Update(ctx context.Context, item *Conversation) error
	UpdateLastMessageAt(ctx context.Context, ownerID, id int64) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type MessageRepository interface {
	Create(ctx context.Context, message *Message) error
	ListByConversation(ctx context.Context, ownerID, conversationID int64) ([]Message, error)
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]Message, error)
	ListByRun(ctx context.Context, ownerID, runID int64) ([]Message, error)
	CreateReferences(ctx context.Context, refs []MessageReference) error
	ListReferencesByMessage(ctx context.Context, ownerID, messageID int64) ([]MessageReference, error)
}
