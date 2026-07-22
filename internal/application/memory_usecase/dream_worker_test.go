package memory_usecase

import (
	"context"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type fakeDreamChatClient struct{ content string }

func (f fakeDreamChatClient) Chat(context.Context, llm.ChatProviderConfig, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: f.content}, nil
}

func (f fakeDreamChatClient) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

type fakeDreamMessages struct {
	items        []conversation.Message
	archivedCall int
}

func (f *fakeDreamMessages) ListActiveByConversation(context.Context, int64, int64) ([]conversation.Message, error) {
	return append([]conversation.Message(nil), f.items...), nil
}

func (f *fakeDreamMessages) ListActiveThrough(_ context.Context, _, _, throughMessageID int64) ([]conversation.Message, error) {
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if item.ID <= throughMessageID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *fakeDreamMessages) ArchiveConversationMessages(context.Context, int64, int64, time.Time) (int64, error) {
	f.archivedCall++
	return int64(len(f.items)), nil
}

func (f *fakeDreamMessages) ArchiveConversationMessagesThrough(_ context.Context, _, _, throughMessageID int64, _ time.Time) (int64, error) {
	f.archivedCall++
	var count int64
	for _, item := range f.items {
		if item.ID <= throughMessageID {
			count++
		}
	}
	return count, nil
}

type fakeDreamMemoryRepo struct{ items map[int64]*memory.Memory }

func (f *fakeDreamMemoryRepo) Create(_ context.Context, item *memory.Memory) error {
	if f.items == nil {
		f.items = map[int64]*memory.Memory{}
	}
	if item.ID == 0 {
		if item.SourceKey != nil {
			for _, existing := range f.items {
				if existing.SourceKey != nil && *existing.SourceKey == *item.SourceKey {
					item.ID = existing.ID
					return nil
				}
			}
		}
		item.ID = int64(len(f.items) + 1)
	}
	clone := *item
	f.items[item.ID] = &clone
	return nil
}
func (f *fakeDreamMemoryRepo) Update(_ context.Context, item *memory.Memory) error {
	clone := *item
	f.items[item.ID] = &clone
	return nil
}
func (f *fakeDreamMemoryRepo) FindByID(_ context.Context, ownerID, id int64) (*memory.Memory, error) {
	clone := *f.items[id]
	return &clone, nil
}
func (f *fakeDreamMemoryRepo) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(ids))
	for _, id := range ids {
		if item, ok := f.items[id]; ok && item.OwnerID == ownerID {
			items = append(items, *item)
		}
	}
	return items, nil
}
func (f *fakeDreamMemoryRepo) List(context.Context, int64, []string, *int64, int, int) ([]memory.Memory, error) {
	return nil, nil
}
func (f *fakeDreamMemoryRepo) ListForRead(_ context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(f.items))
	for _, item := range f.items {
		if item.OwnerID == ownerID {
			items = append(items, *item)
		}
	}
	return items, nil
}
func (f *fakeDreamMemoryRepo) ListByLevel(context.Context, int64, string, []string, int) ([]memory.Memory, error) {
	return nil, nil
}
func (f *fakeDreamMemoryRepo) ListActiveOwnerIDs(context.Context, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeDreamMemoryRepo) IncrementAccessCount(context.Context, int64, int64) error { return nil }
func (f *fakeDreamMemoryRepo) IncrementConsolidationCount(context.Context, int64, int64) error {
	return nil
}
func (f *fakeDreamMemoryRepo) SoftDelete(context.Context, int64, int64) error { return nil }
func (f *fakeDreamMemoryRepo) MarkUsed(context.Context, int64, []int64) error { return nil }
func (f *fakeDreamMemoryRepo) MarkExpired(context.Context, int64, int) (int64, error) {
	return 0, nil
}
func (f *fakeDreamMemoryRepo) UpdateDecayedImportance(context.Context, int64, float64) (int64, error) {
	return 0, nil
}
func (f *fakeDreamMemoryRepo) SetEmbedding(context.Context, int64, int64, []byte) error { return nil }

func TestDreamWorkerHandlesConversationAndArchivesMessages(t *testing.T) {
	redisServer := miniredis.RunT(t)
	defer redisServer.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer redisClient.Close()
	repo := &fakeDreamMemoryRepo{items: map[int64]*memory.Memory{1: {ID: 1, OwnerID: 1, MemoryType: memory.TypeProfile, Content: "Existing preference", Importance: 0.9}}}
	messages := &fakeDreamMessages{items: []conversation.Message{{ID: 1, OwnerID: 1, ConversationID: 10, Role: conversation.RoleUser, Content: "我喜欢简洁回答"}}}
	worker := NewDreamWorker(fakeDreamChatClient{content: `{"core_updates":[{"memory_type":"profile_memory","title":"style","content":"User prefers concise answers","action":"create"}],"archival_inserts":[{"content":"User discussed answer style preference"}]}`}, nil, repo, nil, messages, nil, redisClient, DreamConfig{Enabled: true, Model: "dream-model"}, "worker-1")
	if err := worker.HandleDreamJob(context.Background(), DreamPayload{OwnerID: 1, ConversationID: 10}); err != nil {
		t.Fatal(err)
	}
	if messages.archivedCall != 1 {
		t.Fatalf("expected archive call, got %d", messages.archivedCall)
	}
	if len(repo.items) < 3 {
		t.Fatalf("expected core and archival memories to be stored, got %+v", repo.items)
	}
}

func TestDreamWorkerJobRetryIsIdempotent(t *testing.T) {
	repo := &fakeDreamMemoryRepo{items: map[int64]*memory.Memory{}}
	messages := &fakeDreamMessages{items: []conversation.Message{{ID: 1, OwnerID: 1, ConversationID: 10, Role: conversation.RoleUser, Content: "remember this"}}}
	jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{7: {
		ID: 7, OwnerID: 1, ConversationID: 10, ThroughMessageID: 1, Status: string(memory.ExtractionPending),
	}}}
	worker := NewDreamWorker(fakeDreamChatClient{content: `{"core_updates":[{"memory_type":"profile_memory","content":"fact","action":"create"}],"archival_inserts":[{"content":"episode"}]}`}, nil, repo, nil, messages, nil, nil, DreamConfig{Enabled: true, Model: "dream-model"}, "worker", jobs)
	payload := DreamPayload{JobID: 7, OwnerID: 1, ConversationID: 10}
	if err := worker.HandleDreamJob(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	count := len(repo.items)
	if err := worker.HandleDreamJob(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != count || messages.archivedCall != 1 || jobs.jobs[7].Status != string(memory.ExtractionCompleted) {
		t.Fatalf("dream retry duplicated effects: memories=%d archived=%d job=%+v", len(repo.items), messages.archivedCall, jobs.jobs[7])
	}
}
