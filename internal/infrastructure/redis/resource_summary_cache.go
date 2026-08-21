package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"agentcanvas/internal/domain/resource"

	"github.com/google/uuid"
	redisclient "github.com/redis/go-redis/v9"
)

type ResourceSummaryCache struct {
	client  *redisclient.Client
	next    resource.Query
	prefix  string
	ttl     time.Duration
	log     *slog.Logger
	mu      sync.Mutex
	flights map[string]*resourceFlight
}

type resourceFlight struct {
	done    chan struct{}
	ownerID int64
	kind    resource.Kind
	page    resource.Page
	err     error
}

type resourceCacheEnvelope struct {
	Schema  int           `json:"schema"`
	OwnerID int64         `json:"owner_id"`
	Kind    resource.Kind `json:"kind"`
	Page    resource.Page `json:"page"`
}

func NewResourceSummaryCache(client *redisclient.Client, next resource.Query, prefix string, ttl time.Duration, log *slog.Logger) *ResourceSummaryCache {
	return &ResourceSummaryCache{client: client, next: next, prefix: prefix, ttl: ttl, log: log, flights: make(map[string]*resourceFlight)}
}

func (c *ResourceSummaryCache) List(ctx context.Context, ownerID int64, kind resource.Kind, options resource.ListOptions) (resource.Page, error) {
	options.Limit = normalizeResourceLimit(options.Limit)
	cacheCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	epoch, epochErr := c.epoch(cacheCtx, ownerID, kind)
	cancel()
	if epochErr != nil {
		c.log.Warn("resource cache epoch read failed", "kind", kind, "error", epochErr)
		epoch = "redis-unavailable"
	}
	key := c.key(ownerID, kind, epoch, options)
	cacheCtx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
	data, err := c.client.Get(cacheCtx, key).Bytes()
	cancel()
	if err == nil {
		var envelope resourceCacheEnvelope
		if json.Unmarshal(data, &envelope) == nil && envelope.Schema == 1 && envelope.OwnerID == ownerID && envelope.Kind == kind {
			return envelope.Page, nil
		}
		c.log.Warn("resource cache entry invalid", "kind", kind, "key_hash", shortHash(key))
	} else if err != redisclient.Nil {
		c.log.Warn("resource cache read failed", "kind", kind, "error", err)
	}

	page, err := c.load(ctx, c.key(ownerID, kind, "flight", options), key, ownerID, kind, options)
	if err != nil {
		return resource.Page{}, err
	}
	return page, nil
}

func (c *ResourceSummaryCache) load(ctx context.Context, flightKey, cacheKey string, ownerID int64, kind resource.Kind, options resource.ListOptions) (resource.Page, error) {
	c.mu.Lock()
	if existing := c.flights[flightKey]; existing != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return resource.Page{}, ctx.Err()
		case <-existing.done:
			return existing.page, existing.err
		}
	}
	flight := &resourceFlight{done: make(chan struct{}), ownerID: ownerID, kind: kind}
	c.flights[flightKey] = flight
	c.mu.Unlock()

	// A caller can observe a miss just before the previous leader stores and
	// removes its flight. Recheck after becoming leader to close that window.
	cacheCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	if data, cacheErr := c.client.Get(cacheCtx, cacheKey).Bytes(); cacheErr == nil {
		var envelope resourceCacheEnvelope
		if json.Unmarshal(data, &envelope) == nil && envelope.Schema == 1 && envelope.OwnerID == ownerID && envelope.Kind == kind {
			cancel()
			flight.page = envelope.Page
			c.finishFlight(flightKey, flight)
			return envelope.Page, nil
		}
	}
	cancel()

	page, err := c.next.List(ctx, ownerID, kind, options)
	if err == nil {
		c.store(cacheKey, ownerID, kind, page)
	}
	flight.page, flight.err = page, err
	c.finishFlight(flightKey, flight)
	return page, err
}

func (c *ResourceSummaryCache) finishFlight(key string, flight *resourceFlight) {
	c.mu.Lock()
	close(flight.done)
	c.mu.Unlock()
	time.AfterFunc(100*time.Millisecond, func() {
		c.mu.Lock()
		if c.flights[key] == flight {
			delete(c.flights, key)
		}
		c.mu.Unlock()
	})
}

func (c *ResourceSummaryCache) store(key string, ownerID int64, kind resource.Kind, page resource.Page) {
	data, err := json.Marshal(resourceCacheEnvelope{Schema: 1, OwnerID: ownerID, Kind: kind, Page: page})
	if err == nil && len(data) <= 256*1024 {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		ttl := c.ttl + time.Duration(rand.Int63n(int64(c.ttl/5)+1)) - c.ttl/10
		if setErr := c.client.Set(cacheCtx, key, data, ttl).Err(); setErr != nil {
			c.log.Warn("resource cache write failed", "kind", kind, "error", setErr)
		}
		cancel()
	}
}

func (c *ResourceSummaryCache) Invalidate(ctx context.Context, ownerID int64, kind resource.Kind) error {
	cacheCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err := c.client.Set(cacheCtx, c.epochKey(ownerID, kind), uuid.NewString(), 0).Err(); err != nil {
		return err
	}
	c.mu.Lock()
	for key, flight := range c.flights {
		if flight.ownerID == ownerID && flight.kind == kind {
			delete(c.flights, key)
		}
	}
	c.mu.Unlock()
	return nil
}

func (c *ResourceSummaryCache) epoch(ctx context.Context, ownerID int64, kind resource.Kind) (string, error) {
	key := c.epochKey(ownerID, kind)
	epoch, err := c.client.Get(ctx, key).Result()
	if err == nil {
		return epoch, nil
	}
	if err != redisclient.Nil {
		return "", err
	}
	epoch = uuid.NewString()
	created, err := c.client.SetNX(ctx, key, epoch, 0).Result()
	if err != nil {
		return "", err
	}
	if created {
		return epoch, nil
	}
	return c.client.Get(ctx, key).Result()
}

func (c *ResourceSummaryCache) epochKey(ownerID int64, kind resource.Kind) string {
	return fmt.Sprintf("%s:resource:v1:epoch:%d:%s", c.prefix, ownerID, kind)
}

func (c *ResourceSummaryCache) key(ownerID int64, kind resource.Kind, epoch string, options resource.ListOptions) string {
	query := fmt.Sprintf("limit=%d&cursor=%s", options.Limit, options.Cursor)
	return fmt.Sprintf("%s:resource:v1:data:%d:%s:%s:%s", c.prefix, ownerID, kind, epoch, shortHash(query))
}

func normalizeResourceLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
