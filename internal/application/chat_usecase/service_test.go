package chat_usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/dialog"
	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/usage"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func TestChatCreatesConversationMessagesReferencesAndUsage(t *testing.T) {
	service, fakes := newTestService(t)
	resp, err := service.Chat(context.Background(), 1, 30, ChatRequest{ProviderID: 10, KBIDs: []int64{20}, Question: "What is RAG?"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Conversation.ID == 0 || resp.UserMessage.ID == 0 || resp.AssistantMessage.ID == 0 {
		t.Fatalf("missing persisted ids: %+v", resp)
	}
	if resp.Conversation.DialogID == nil || *resp.Conversation.DialogID != 30 {
		t.Fatalf("expected dialog id to be persisted: %+v", resp.Conversation)
	}
	if len(resp.References) != 1 || resp.References[0].MessageID != resp.AssistantMessage.ID {
		t.Fatalf("unexpected references: %+v", resp.References)
	}
	if len(fakes.usage.items) != 1 || !fakes.usage.items[0].Success || fakes.usage.items[0].TotalTokens != 6 {
		t.Fatalf("unexpected usage: %+v", fakes.usage.items)
	}
	if fakes.retriever.calls != 1 || fakes.llm.calls != 1 {
		t.Fatalf("unexpected calls retriever=%d llm=%d", fakes.retriever.calls, fakes.llm.calls)
	}
}

func TestChatInvalidInputDoesNotCallDependencies(t *testing.T) {
	service, fakes := newTestService(t)
	_, err := service.Chat(context.Background(), 1, 30, ChatRequest{ProviderID: 10, Question: "missing kb"})
	if !errors.Is(err, agenterrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
	if fakes.retriever.calls != 0 || fakes.llm.calls != 0 {
		t.Fatalf("dependencies were called retriever=%d llm=%d", fakes.retriever.calls, fakes.llm.calls)
	}
}

func TestChatLLMFailureKeepsUserMessageAndWritesFailedUsage(t *testing.T) {
	service, fakes := newTestService(t)
	fakes.llm.err = errors.New("provider down")
	_, err := service.Chat(context.Background(), 1, 30, ChatRequest{ProviderID: 10, KBIDs: []int64{20}, Question: "What is RAG?"})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(fakes.messages.items) != 1 || fakes.messages.items[0].Role != conversation.RoleUser {
		t.Fatalf("unexpected messages: %+v", fakes.messages.items)
	}
	if len(fakes.usage.items) != 1 || fakes.usage.items[0].Success {
		t.Fatalf("expected failed usage: %+v", fakes.usage.items)
	}
}

func TestChatIncludesConversationHistory(t *testing.T) {
	service, fakes := newTestService(t)
	dialogID := int64(30)
	conv := conversation.Conversation{ID: 7, OwnerID: 1, DialogID: &dialogID, Title: "history"}
	fakes.convs.items = []conversation.Conversation{conv}
	fakes.messages.items = []conversation.Message{
		{ID: 1, OwnerID: 1, ConversationID: 7, Role: conversation.RoleUser, Content: "之前的问题"},
		{ID: 2, OwnerID: 1, ConversationID: 7, Role: conversation.RoleAssistant, Content: "之前的回答"},
	}

	_, err := service.Chat(context.Background(), 1, dialogID, ChatRequest{ProviderID: 10, KBIDs: []int64{20}, ConversationID: 7, Question: "新的问题"})
	if err != nil {
		t.Fatal(err)
	}
	messages := fakes.llm.request.Messages
	if len(messages) < 4 {
		t.Fatalf("expected system + history + current question, got %+v", messages)
	}
	if messages[1].Content != "之前的问题" || messages[2].Content != "之前的回答" || messages[len(messages)-1].Content != "新的问题" {
		t.Fatalf("conversation history was not preserved: %+v", messages)
	}
}

func TestListConversationsRequiresDialogScope(t *testing.T) {
	service, fakes := newTestService(t)
	dialogID := int64(30)
	otherDialogID := int64(31)
	fakes.convs.items = []conversation.Conversation{
		{ID: 1, OwnerID: 1, DialogID: &dialogID, Title: "in dialog"},
		{ID: 2, OwnerID: 1, DialogID: &otherDialogID, Title: "other dialog"},
	}
	items, err := service.ListConversations(context.Background(), 1, dialogID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("expected only dialog scoped conversations, got %+v", items)
	}
}

func TestGetConversationRejectsWrongDialog(t *testing.T) {
	service, fakes := newTestService(t)
	dialogID := int64(31)
	fakes.dialogs.items[dialogID] = &dialog.Dialog{ID: dialogID, OwnerID: 1, Name: "other"}
	fakes.convs.items = []conversation.Conversation{{ID: 1, OwnerID: 1, DialogID: &dialogID, Title: "other dialog"}}
	_, err := service.GetConversation(context.Background(), 1, 30, 1)
	if !errors.Is(err, agenterrors.ErrNotFound) {
		t.Fatalf("expected not found for cross-dialog conversation, got %v", err)
	}
}

type testFakes struct {
	providers *fakeProviderRepo
	dialogs   *fakeDialogRepo
	kbs       *fakeKBRepo
	convs     *fakeConversationRepo
	messages  *fakeMessageRepo
	usage     *fakeUsageRepo
	retriever *fakeRetriever
	llm       *fakeChatClient
}

func newTestService(t *testing.T) (*Service, testFakes) {
	t.Helper()
	secrets, err := cryptoinfra.NewSecretBox("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Encrypt("test-key")
	if err != nil {
		t.Fatal(err)
	}
	fakes := testFakes{
		providers: &fakeProviderRepo{item: &providerdomain.ModelProvider{ID: 10, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "http://example.com", DefaultChatModel: "gpt-test", Status: providerdomain.StatusActive, EncryptedAPIKey: encrypted}},
		dialogs:   &fakeDialogRepo{items: map[int64]*dialog.Dialog{30: {ID: 30, OwnerID: 1, Name: "dlg"}}},
		kbs:       &fakeKBRepo{items: map[int64]*knowledge.KnowledgeBase{20: {ID: 20, OwnerID: 1, Status: knowledge.KnowledgeBaseStatusActive}}},
		convs:     &fakeConversationRepo{},
		messages:  &fakeMessageRepo{},
		usage:     &fakeUsageRepo{},
		retriever: &fakeRetriever{resp: &retrieval.RetrievalResponse{LatencyMS: 12, Results: []retrieval.RetrievalResult{{ChunkID: 30, DocumentID: 40, KBID: 20, Score: 0.9, Content: "RAG means retrieval augmented generation.", DocumentName: "rag.md"}}}},
		llm:       &fakeChatClient{resp: &llm.ChatResponse{Content: "RAG means retrieval augmented generation.", Usage: llm.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6}}},
	}
	return NewService(fakes.providers, fakes.dialogs, fakes.kbs, fakes.convs, fakes.messages, fakes.usage, fakes.retriever, fakes.llm, secrets), fakes
}

type fakeProviderRepo struct{ item *providerdomain.ModelProvider }

func (r *fakeProviderRepo) Create(context.Context, *providerdomain.ModelProvider) error { return nil }
func (r *fakeProviderRepo) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}
func (r *fakeProviderRepo) FindByID(_ context.Context, ownerID, id int64) (*providerdomain.ModelProvider, error) {
	if r.item != nil && r.item.OwnerID == ownerID && r.item.ID == id {
		return r.item, nil
	}
	return nil, errors.New("not found")
}
func (r *fakeProviderRepo) Update(context.Context, *providerdomain.ModelProvider) error { return nil }
func (r *fakeProviderRepo) SoftDelete(context.Context, int64, int64) error              { return nil }

type fakeDialogRepo struct {
	items map[int64]*dialog.Dialog
}

func (r *fakeDialogRepo) Create(context.Context, *dialog.Dialog) error { return nil }
func (r *fakeDialogRepo) ListByOwner(context.Context, int64) ([]dialog.Dialog, error) {
	items := make([]dialog.Dialog, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, *item)
	}
	return items, nil
}
func (r *fakeDialogRepo) FindByID(_ context.Context, ownerID, id int64) (*dialog.Dialog, error) {
	if item, ok := r.items[id]; ok && item.OwnerID == ownerID {
		return item, nil
	}
	return nil, errors.New("not found")
}
func (r *fakeDialogRepo) Update(context.Context, *dialog.Dialog) error   { return nil }
func (r *fakeDialogRepo) SoftDelete(context.Context, int64, int64) error { return nil }

type fakeKBRepo struct {
	items map[int64]*knowledge.KnowledgeBase
}

func (r *fakeKBRepo) Create(context.Context, *knowledge.KnowledgeBase) error { return nil }
func (r *fakeKBRepo) ListByOwner(context.Context, int64) ([]knowledge.KnowledgeBase, error) {
	return nil, nil
}
func (r *fakeKBRepo) FindByID(_ context.Context, ownerID, id int64) (*knowledge.KnowledgeBase, error) {
	if item, ok := r.items[id]; ok && item.OwnerID == ownerID {
		return item, nil
	}
	return nil, errors.New("not found")
}
func (r *fakeKBRepo) Update(context.Context, *knowledge.KnowledgeBase) error { return nil }
func (r *fakeKBRepo) SoftDelete(context.Context, int64, int64) error         { return nil }
func (r *fakeKBRepo) AdjustCounts(context.Context, int64, int64, int, int) error {
	return nil
}

type fakeConversationRepo struct {
	nextID int64
	items  []conversation.Conversation
}

func (r *fakeConversationRepo) Create(_ context.Context, item *conversation.Conversation) error {
	r.nextID++
	item.ID = r.nextID
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	r.items = append(r.items, *item)
	return nil
}
func (r *fakeConversationRepo) ListByOwner(context.Context, int64) ([]conversation.Conversation, error) {
	return r.items, nil
}
func (r *fakeConversationRepo) ListByDialog(_ context.Context, ownerID, dialogID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.DialogID != nil && *item.DialogID == dialogID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *fakeConversationRepo) ListByWorkflow(_ context.Context, ownerID, workflowID int64) ([]conversation.Conversation, error) {
	var items []conversation.Conversation
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.WorkflowID != nil && *item.WorkflowID == workflowID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *fakeConversationRepo) FindByID(_ context.Context, ownerID, id int64) (*conversation.Conversation, error) {
	for i := range r.items {
		if r.items[i].OwnerID == ownerID && r.items[i].ID == id {
			return &r.items[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (r *fakeConversationRepo) Update(context.Context, *conversation.Conversation) error { return nil }
func (r *fakeConversationRepo) UpdateLastMessageAt(context.Context, int64, int64) error  { return nil }
func (r *fakeConversationRepo) SoftDelete(context.Context, int64, int64) error           { return nil }

type fakeMessageRepo struct {
	nextID int64
	items  []conversation.Message
	refs   []conversation.MessageReference
}

func (r *fakeMessageRepo) Create(_ context.Context, message *conversation.Message) error {
	r.nextID++
	message.ID = r.nextID
	r.items = append(r.items, *message)
	return nil
}
func (r *fakeMessageRepo) ListByConversation(_ context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	var items []conversation.Message
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ConversationID == conversationID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *fakeMessageRepo) ListActiveByConversation(_ context.Context, ownerID, conversationID int64) ([]conversation.Message, error) {
	return r.ListByConversation(context.Background(), ownerID, conversationID)
}
func (r *fakeMessageRepo) ListByRun(_ context.Context, ownerID, runID int64) ([]conversation.Message, error) {
	var items []conversation.Message
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.RunID != nil && *item.RunID == runID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *fakeMessageRepo) CreateReferences(_ context.Context, refs []conversation.MessageReference) error {
	r.refs = append(r.refs, refs...)
	return nil
}
func (r *fakeMessageRepo) ListReferencesByMessage(_ context.Context, ownerID, messageID int64) ([]conversation.MessageReference, error) {
	var refs []conversation.MessageReference
	for _, ref := range r.refs {
		if ref.OwnerID == ownerID && ref.MessageID == messageID {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

type fakeUsageRepo struct{ items []usage.ModelUsageLog }

func (r *fakeUsageRepo) Create(_ context.Context, log *usage.ModelUsageLog) error {
	r.items = append(r.items, *log)
	return nil
}

type fakeRetriever struct {
	resp  *retrieval.RetrievalResponse
	err   error
	calls int
}

func (r *fakeRetriever) Search(context.Context, retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	r.calls++
	return r.resp, r.err
}

type fakeChatClient struct {
	resp    *llm.ChatResponse
	err     error
	calls   int
	request llm.ChatRequest
}

func (c *fakeChatClient) Chat(_ context.Context, _ llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.calls++
	c.request = req
	return c.resp, c.err
}
func (c *fakeChatClient) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}
