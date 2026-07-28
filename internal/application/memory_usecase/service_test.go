package memory_usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain/memory"
)

var errNotFound = errors.New("record not found")

type fakeCacheStore struct {
	mu    sync.Mutex
	store map[string]fakeCacheEntry
	logs  []string
	err   error
}

type fakeCacheEntry struct {
	items     []memory.Memory
	expiresAt time.Time
}

func (c *fakeCacheStore) Get(ctx context.Context, ownerID int64, key string) ([]memory.Memory, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.store["fake"]
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false, nil
	}
	c.logs = append(c.logs, "get:"+key)
	return entry.items, true, nil
}

func (c *fakeCacheStore) Set(ctx context.Context, ownerID int64, key string, items []memory.Memory, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store["fake"] = fakeCacheEntry{
		items:     append([]memory.Memory{}, items...),
		expiresAt: time.Now().Add(ttl),
	}
	c.logs = append(c.logs, "set:"+key)
	return nil
}

func (c *fakeCacheStore) InvalidateOwner(ctx context.Context, ownerID int64) error {
	if c.err != nil {
		return c.err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = map[string]fakeCacheEntry{}
	c.logs = append(c.logs, "invalidate_owner")
	return nil
}

func (c *fakeCacheStore) InvalidateItem(ctx context.Context, ownerID, id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, "fake")
	c.logs = append(c.logs, "invalidate_item")
	return nil
}

func (c *fakeCacheStore) Close() error { return nil }

type fakeServiceRetriever struct {
	indexed []memory.Memory
	deleted []int64
	err     error
}

func (r *fakeServiceRetriever) Index(ctx context.Context, item memory.Memory) error {
	if r.err != nil {
		return r.err
	}
	r.indexed = append(r.indexed, item)
	return nil
}

func (r *fakeServiceRetriever) Search(ctx context.Context, ownerID int64, query string, memoryTypes []string, limit int) ([]int64, error) {
	return nil, nil
}

func (r *fakeServiceRetriever) Delete(ctx context.Context, memoryID int64) error {
	if r.err != nil {
		return r.err
	}
	r.deleted = append(r.deleted, memoryID)
	return nil
}

type fakeMemRepo struct {
	items     map[int64]*memory.Memory
	nextID    int64
	created   []*memory.Memory
	deleted   []int64
	createErr error
	updateErr error
}

func (r *fakeMemRepo) Create(ctx context.Context, item *memory.Memory) error {
	if r.createErr != nil {
		return r.createErr
	}
	if r.items == nil {
		r.items = map[int64]*memory.Memory{}
	}
	r.nextID++
	item.ID = r.nextID
	clone := *item
	r.items[item.ID] = &clone
	r.created = append(r.created, item)
	return nil
}

func (r *fakeMemRepo) Update(ctx context.Context, item *memory.Memory) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	if r.items == nil {
		r.items = map[int64]*memory.Memory{}
	}
	clone := *item
	r.items[item.ID] = &clone
	return nil
}

func (r *fakeMemRepo) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, errNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeMemRepo) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(ids))
	for _, id := range ids {
		item, err := r.FindByID(ctx, ownerID, id)
		if err == nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeMemRepo) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	var result []memory.Memory
	for _, m := range r.items {
		if m.OwnerID != ownerID {
			continue
		}
		if len(memoryTypes) > 0 {
			found := false
			for _, t := range memoryTypes {
				if m.MemoryType == t {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, *m)
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (r *fakeMemRepo) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	return r.List(ctx, ownerID, memoryTypes, conversationID, limit, 0)
}

func (r *fakeMemRepo) SoftDelete(ctx context.Context, ownerID, id int64) error {
	r.deleted = append(r.deleted, id)
	delete(r.items, id)
	return nil
}

func (r *fakeMemRepo) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	return nil
}

func (r *fakeMemRepo) ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]memory.Memory, error) {
	var result []memory.Memory
	for _, m := range r.items {
		if ownerID > 0 && m.OwnerID != ownerID {
			continue
		}
		if m.MemoryLevel != level {
			continue
		}
		if len(memoryTypes) > 0 {
			found := false
			for _, t := range memoryTypes {
				if m.MemoryType == t {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, *m)
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (r *fakeMemRepo) ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error) {
	seen := map[int64]bool{}
	ids := make([]int64, 0)
	for _, m := range r.items {
		if m.OwnerID > 0 && !seen[m.OwnerID] {
			seen[m.OwnerID] = true
			ids = append(ids, m.OwnerID)
		}
	}
	return ids, nil
}
func (r *fakeMemRepo) IncrementAccessCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeMemRepo) IncrementConsolidationCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeMemRepo) MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	var count int64
	for _, m := range r.items {
		if m.ExpiresAt != nil && time.Now().After(*m.ExpiresAt) {
			delete(r.items, m.ID)
			r.deleted = append(r.deleted, m.ID)
			count++
		}
	}
	return count, nil
}
func (r *fakeMemRepo) UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	return 0, nil
}
func (r *fakeMemRepo) SetEmbedding(ctx context.Context, ownerID, id int64, embedding []byte) error {
	return nil
}

var _ memory.Repository = (*fakeMemRepo)(nil)

func TestServiceCreateInvalidatesCache(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	cache := &fakeCacheStore{store: map[string]fakeCacheEntry{}}
	svc := NewServiceWithCache(repo, cache)

	_, err := svc.Create(context.Background(), 100, CreateMemoryRequest{
		MemoryType: memory.TypeProfile,
		Content:    "test content",
	})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, l := range cache.logs {
		if l == "invalidate_owner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cache invalidation on create")
	}
}

func TestServiceListUsesCacheAndFallsBack(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	repo.Create(context.Background(), &memory.Memory{OwnerID: 100, MemoryType: memory.TypeProfile, Content: "from db"})

	cache := &fakeCacheStore{store: map[string]fakeCacheEntry{}}
	svcWithoutCache := NewService(repo)
	svcWithCache := NewServiceWithCache(repo, cache)

	itemsNoCache, _ := svcWithoutCache.List(context.Background(), 100, nil, nil, 50, 0)
	if len(itemsNoCache) != 1 {
		t.Fatalf("expected 1 item from db, got %d", len(itemsNoCache))
	}

	cached := []memory.Memory{{ID: 99, OwnerID: 100, MemoryType: memory.TypeProfile, Content: "from cache"}}
	cache.Set(context.Background(), 100, "list::_:50:0", cached, time.Minute)

	itemsWithCache, err := svcWithCache.List(context.Background(), 100, nil, nil, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsWithCache) != 1 || itemsWithCache[0].Content != "from cache" {
		t.Fatalf("expected cached item, got %+v", itemsWithCache)
	}
}

func TestServiceUpdateInvalidatesCache(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{
		1: {ID: 1, OwnerID: 100, MemoryType: memory.TypeProfile, Content: "old"},
	}}
	cache := &fakeCacheStore{store: map[string]fakeCacheEntry{}}
	svc := NewServiceWithCache(repo, cache)

	importance := 0.9
	_, err := svc.Update(context.Background(), 100, 1, UpdateMemoryRequest{
		Content:    "new",
		Importance: &importance,
	})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, l := range cache.logs {
		if l == "invalidate_owner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cache invalidation on update")
	}

	item, _ := repo.FindByID(context.Background(), 100, 1)
	if item.Content != "new" {
		t.Fatalf("expected content 'new', got '%s'", item.Content)
	}
}

func TestServiceDeleteInvalidatesCache(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{
		1: {ID: 1, OwnerID: 100, MemoryType: memory.TypeProfile, Content: "test"},
	}}
	cache := &fakeCacheStore{store: map[string]fakeCacheEntry{}}
	svc := NewServiceWithCache(repo, cache)

	err := svc.Delete(context.Background(), 100, 1)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, l := range cache.logs {
		if l == "invalidate_owner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cache invalidation on delete")
	}

	item, err := repo.FindByID(context.Background(), 100, 1)
	if err != nil || item.Status != memory.StatusRevoked {
		t.Fatalf("expected auditable revoked version after delete: item=%+v err=%v", item, err)
	}
}

func TestServiceCreateIgnoresCacheInvalidationError(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	cache := &fakeCacheStore{store: map[string]fakeCacheEntry{}, err: errors.New("redis unavailable")}
	svc := NewServiceWithCache(repo, cache)

	item, err := svc.Create(context.Background(), 100, CreateMemoryRequest{MemoryType: memory.TypeProfile, Content: "test"})
	if err != nil {
		t.Fatalf("database write must remain successful when cache is unavailable: %v", err)
	}
	if item == nil || item.ID == 0 {
		t.Fatal("expected created memory")
	}
}

func TestServiceCreateUsesTransactionalOutboxInsteadOfLegacyIndex(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	retriever := &fakeServiceRetriever{}
	svc := NewServiceWithCacheAndRetriever(repo, nil, retriever)

	item, err := svc.Create(context.Background(), 100, CreateMemoryRequest{MemoryType: memory.TypeProfile, Content: "index me"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == 0 || len(retriever.indexed) != 0 {
		t.Fatalf("legacy index must remain shadow-read only: item=%+v indexed=%+v", item, retriever.indexed)
	}
}

func TestServiceDeleteUsesTransactionalOutboxInsteadOfLegacyIndex(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{1: {ID: 1, OwnerID: 100, MemoryType: memory.TypeProfile, Content: "delete me"}}}
	retriever := &fakeServiceRetriever{}
	svc := NewServiceWithCacheAndRetriever(repo, nil, retriever)

	if err := svc.Delete(context.Background(), 100, 1); err != nil {
		t.Fatal(err)
	}
	if len(retriever.deleted) != 0 {
		t.Fatalf("legacy index must not receive delete writes: %v", retriever.deleted)
	}
}

func TestNewServiceWithoutCacheWorks(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	svc := NewService(repo)

	_, err := svc.Create(context.Background(), 100, CreateMemoryRequest{
		MemoryType: memory.TypeProfile,
		Content:    "no cache test",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := svc.List(context.Background(), 100, nil, nil, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}
