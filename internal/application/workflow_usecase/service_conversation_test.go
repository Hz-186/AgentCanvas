package workflow_usecase

import (
	"context"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

func TestWorkflowConversationIsScopedToWorkflow(t *testing.T) {
	conversations := &fakeWorkflowConversationRepo{}
	service := &Service{
		workflows:     &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "Research", Status: workflow.StatusActive}}},
		conversations: conversations,
	}
	created, err := service.CreateWorkflowConversation(context.Background(), 1, 20, CreateWorkflowConversationRequest{Title: " Test conversation "})
	if err != nil {
		t.Fatal(err)
	}
	if created.Source != conversation.SourceWorkflow || created.WorkflowID == nil || *created.WorkflowID != 20 || created.Title != "Test conversation" {
		t.Fatalf("unexpected conversation: %+v", created)
	}
	items, err := service.ListWorkflowConversations(context.Background(), 1, 20)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("unexpected list: %+v err=%v", items, err)
	}
	if _, err := service.GetWorkflowConversation(context.Background(), 1, 21, created.ID); err == nil {
		t.Fatal("expected cross-workflow access to fail")
	}
}

func TestEnsureWorkflowAssistantMessageReusesMessageNodeOutput(t *testing.T) {
	runID := int64(44)
	messages := &fakeWorkflowMessageRepo{items: []conversation.Message{{
		ID: 9, OwnerID: 1, ConversationID: 7, Role: conversation.RoleAssistant, Content: "written by message node", RunID: &runID,
	}}}
	service := &Service{messages: messages}

	message, err := service.ensureWorkflowAssistantMessage(context.Background(), 1, 7, runID, map[string]any{"content": "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != 9 || len(messages.items) != 1 {
		t.Fatalf("expected existing assistant message, got %+v items=%+v", message, messages.items)
	}
}

type fakeWorkflowConversationRepo struct {
	items []conversation.Conversation
}

func (r *fakeWorkflowConversationRepo) Create(_ context.Context, item *conversation.Conversation) error {
	item.ID = int64(len(r.items) + 1)
	item.CreatedAt = time.Now().UTC()
	item.UpdatedAt = item.CreatedAt
	r.items = append(r.items, *item)
	return nil
}
func (r *fakeWorkflowConversationRepo) ListByOwner(_ context.Context, ownerID int64) ([]conversation.Conversation, error) {
	return r.filter(ownerID, 0, false), nil
}
func (r *fakeWorkflowConversationRepo) ListByDialog(context.Context, int64, int64) ([]conversation.Conversation, error) {
	return nil, nil
}
func (r *fakeWorkflowConversationRepo) ListByWorkflow(_ context.Context, ownerID, workflowID int64) ([]conversation.Conversation, error) {
	return r.filter(ownerID, workflowID, true), nil
}
func (r *fakeWorkflowConversationRepo) FindByID(_ context.Context, ownerID, id int64) (*conversation.Conversation, error) {
	for i := range r.items {
		if r.items[i].OwnerID == ownerID && r.items[i].ID == id {
			item := r.items[i]
			return &item, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *fakeWorkflowConversationRepo) Update(context.Context, *conversation.Conversation) error {
	return nil
}
func (r *fakeWorkflowConversationRepo) UpdateLastMessageAt(context.Context, int64, int64) error {
	return nil
}
func (r *fakeWorkflowConversationRepo) SoftDelete(context.Context, int64, int64) error { return nil }
func (r *fakeWorkflowConversationRepo) filter(ownerID, workflowID int64, byWorkflow bool) []conversation.Conversation {
	items := make([]conversation.Conversation, 0)
	for _, item := range r.items {
		if item.OwnerID != ownerID || (byWorkflow && (item.WorkflowID == nil || *item.WorkflowID != workflowID)) {
			continue
		}
		items = append(items, item)
	}
	return items
}

type fakeWorkflowMessageRepo struct {
	items []conversation.Message
}

func (r *fakeWorkflowMessageRepo) Create(_ context.Context, item *conversation.Message) error {
	item.ID = int64(len(r.items) + 1)
	r.items = append(r.items, *item)
	return nil
}
func (r *fakeWorkflowMessageRepo) ListByConversation(_ context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	items := make([]conversation.Message, 0)
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ConversationID == conversationID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *fakeWorkflowMessageRepo) ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	return r.ListByConversation(ctx, ownerID, conversationID)
}
func (r *fakeWorkflowMessageRepo) ListByRun(_ context.Context, ownerID, runID int64) ([]conversation.Message, error) {
	items := make([]conversation.Message, 0)
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.RunID != nil && *item.RunID == runID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *fakeWorkflowMessageRepo) CreateReferences(context.Context, []conversation.MessageReference) error {
	return nil
}
func (r *fakeWorkflowMessageRepo) ListReferencesByMessage(context.Context, int64, int64) ([]conversation.MessageReference, error) {
	return nil, nil
}
