package redis

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain/resource"

	"github.com/alicebob/miniredis/v2"
	redisclient "github.com/redis/go-redis/v9"
)

type fakeResourceQuery struct {
	mu    sync.Mutex
	calls int
	page  resource.Page
	err   error
}

func testResourceLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (q *fakeResourceQuery) List(context.Context, int64, resource.Kind, resource.ListOptions) (resource.Page, error) {
	q.mu.Lock()
	q.calls++
	q.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	return q.page, q.err
}

func TestResourceSummaryCacheHitAndInvalidate(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redisclient.NewClient(&redisclient.Options{Addr: mr.Addr()})
	next := &fakeResourceQuery{page: resource.Page{Items: []resource.Summary{{ID: 1, Name: "first"}}}}
	cache := NewResourceSummaryCache(client, next, "test", time.Minute, testResourceLogger())
	ctx := context.Background()
	options := resource.ListOptions{Limit: 25}

	for i := 0; i < 2; i++ {
		page, err := cache.List(ctx, 10, resource.KindSkills, options)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("List() page=%+v err=%v", page, err)
		}
	}
	if next.calls != 1 {
		t.Fatalf("expected one database query, got %d", next.calls)
	}

	next.page = resource.Page{Items: []resource.Summary{{ID: 2, Name: "second"}}}
	if err := cache.Invalidate(ctx, 10, resource.KindSkills); err != nil {
		t.Fatal(err)
	}
	page, err := cache.List(ctx, 10, resource.KindSkills, options)
	if err != nil || page.Items[0].ID != 2 {
		t.Fatalf("expected refreshed page, got %+v err=%v", page, err)
	}
	if next.calls != 2 {
		t.Fatalf("expected database query after invalidation, got %d", next.calls)
	}
}

func TestResourceSummaryCacheOwnerIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redisclient.NewClient(&redisclient.Options{Addr: mr.Addr()})
	next := &fakeResourceQuery{page: resource.Page{Items: []resource.Summary{{ID: 1}}}}
	cache := NewResourceSummaryCache(client, next, "test", time.Minute, testResourceLogger())
	ctx := context.Background()

	_, _ = cache.List(ctx, 10, resource.KindDialogs, resource.ListOptions{})
	_, _ = cache.List(ctx, 20, resource.KindDialogs, resource.ListOptions{})
	if next.calls != 2 {
		t.Fatalf("expected isolated owner keys, got %d database calls", next.calls)
	}
}

func TestResourceSummaryCacheFallsBackWhenRedisUnavailable(t *testing.T) {
	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1", DialTimeout: time.Millisecond})
	next := &fakeResourceQuery{page: resource.Page{Items: []resource.Summary{{ID: 1}}}}
	cache := NewResourceSummaryCache(client, next, "test", time.Minute, testResourceLogger())

	page, err := cache.List(context.Background(), 10, resource.KindWorkflows, resource.ListOptions{})
	if err != nil || len(page.Items) != 1 || next.calls != 1 {
		t.Fatalf("expected database fallback, page=%+v calls=%d err=%v", page, next.calls, err)
	}
}

func TestResourceSummaryCacheCoalescesConcurrentMisses(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redisclient.NewClient(&redisclient.Options{Addr: mr.Addr()})
	next := &fakeResourceQuery{page: resource.Page{Items: []resource.Summary{{ID: 1}}}}
	cache := NewResourceSummaryCache(client, next, "test", time.Minute, testResourceLogger())
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := cache.List(ctx, 10, resource.KindSkills, resource.ListOptions{Limit: 25})
			if err != nil || len(page.Items) != 1 {
				t.Errorf("List() page=%+v err=%v", page, err)
			}
		}()
	}
	wg.Wait()
	if next.calls != 1 {
		t.Fatalf("expected one database query, got %d", next.calls)
	}
}
