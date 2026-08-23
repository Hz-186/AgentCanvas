package redis

import (
	"context"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func testMemory(id, owner int64, content string) memory.Memory {
	return memory.Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: id, OwnerID: owner}}, Content: content}
}

func newTestCache(t *testing.T) (*MemoryCache, func()) {
	t.Helper()
	cache, _, cleanup := newTestCacheWithRedis(t)
	return cache, cleanup
}

func newTestCacheWithRedis(t *testing.T) (*MemoryCache, *miniredis.Miniredis, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := NewMemoryCache(client)
	return cache, mr, func() {
		cache.Close()
		mr.Close()
	}
}

func TestMemoryCache_SetAndGet(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()
	ctx := context.Background()

	items := []memory.Memory{testMemory(1, 100, "user likes Go"), testMemory(2, 100, "user prefers clean code")}
	err := cache.Set(ctx, 100, "list:profile", items, 5*time.Minute)
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}

	got, hit, err := cache.Get(ctx, 100, "list:profile")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !hit {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].Content != "user likes Go" {
		t.Fatalf("unexpected content: %s", got[0].Content)
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()
	ctx := context.Background()

	_, hit, err := cache.Get(ctx, 100, "nonexistent")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if hit {
		t.Fatal("expected cache miss")
	}
}

func TestMemoryCache_InvalidateOwner(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()
	ctx := context.Background()

	items := []memory.Memory{testMemory(1, 100, "test")}
	cache.Set(ctx, 100, "list:all", items, 5*time.Minute)
	cache.Set(ctx, 100, "list:profile", items, 5*time.Minute)

	err := cache.InvalidateOwner(ctx, 100)
	if err != nil {
		t.Fatalf("invalidate failed: %v", err)
	}

	_, hit, _ := cache.Get(ctx, 100, "list:all")
	if hit {
		t.Fatal("expected cache miss after invalidation")
	}
	_, hit, _ = cache.Get(ctx, 100, "list:profile")
	if hit {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestMemoryCache_InvalidateItem(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()
	ctx := context.Background()

	items := []memory.Memory{testMemory(42, 100, "test")}
	cache.Set(ctx, 100, "id:42", items, 5*time.Minute)
	cache.Set(ctx, 100, "list:all", items, 5*time.Minute)

	err := cache.InvalidateItem(ctx, 100, 42)
	if err != nil {
		t.Fatalf("invalidate item failed: %v", err)
	}

	_, hit, _ := cache.Get(ctx, 100, "id:42")
	if hit {
		t.Fatal("expected cache miss for invalidated item")
	}

	_, hit, _ = cache.Get(ctx, 100, "list:all")
	if hit {
		t.Fatal("expected all keys cleared after item invalidation")
	}
}

func TestMemoryCache_TTL(t *testing.T) {
	cache, mr, cleanup := newTestCacheWithRedis(t)
	defer cleanup()
	ctx := context.Background()

	items := []memory.Memory{testMemory(1, 100, "ephemeral")}
	cache.Set(ctx, 100, "ttl", items, 5*time.Second)

	_, hit, _ := cache.Get(ctx, 100, "ttl")
	if !hit {
		t.Fatal("expected immediate hit")
	}

	mr.FastForward(10 * time.Second)

	_, hit, _ = cache.Get(ctx, 100, "ttl")
	if hit {
		t.Fatal("expected expiration after TTL")
	}
}

func TestMemoryCache_DifferentOwnersIsolated(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()
	ctx := context.Background()

	itemsA := []memory.Memory{testMemory(1, 100, "A")}
	itemsB := []memory.Memory{testMemory(2, 200, "B")}

	cache.Set(ctx, 100, "list", itemsA, 5*time.Minute)
	cache.Set(ctx, 200, "list", itemsB, 5*time.Minute)

	got, hit, _ := cache.Get(ctx, 100, "list")
	if !hit || len(got) != 1 || got[0].Content != "A" {
		t.Fatalf("unexpected result for owner 100: %+v", got)
	}

	err := cache.InvalidateOwner(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}

	_, hit, _ = cache.Get(ctx, 100, "list")
	if hit {
		t.Fatal("expected miss for owner 100 after invalidation")
	}

	got, hit, _ = cache.Get(ctx, 200, "list")
	if !hit || len(got) != 1 || got[0].Content != "B" {
		t.Fatal("expected owner 200 to be unaffected")
	}
}
