package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain/memory"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	workingMemoryPrefix = "wm"
	workingMemoryTTL    = 24 * time.Hour
)

type WorkingMemoryRepository struct {
	client   *redis.Client
	ttl      time.Duration
	lockTTL  time.Duration
	lockWait time.Duration
}

type WorkingMemoryOptions struct {
	TTL      time.Duration
	LockTTL  time.Duration
	LockWait time.Duration
}

var ErrWorkingMemoryLockTimeout = fmt.Errorf("working memory lock timeout")

func NewWorkingMemoryRepository(client *redis.Client, configured ...WorkingMemoryOptions) *WorkingMemoryRepository {
	options := WorkingMemoryOptions{TTL: workingMemoryTTL, LockTTL: 5 * time.Second, LockWait: 500 * time.Millisecond}
	if len(configured) > 0 {
		if configured[0].TTL > 0 {
			options.TTL = configured[0].TTL
		}
		if configured[0].LockTTL > 0 {
			options.LockTTL = configured[0].LockTTL
		}
		if configured[0].LockWait > 0 {
			options.LockWait = configured[0].LockWait
		}
	}
	return &WorkingMemoryRepository{client: client, ttl: options.TTL, lockTTL: options.LockTTL, lockWait: options.LockWait}
}

func (r *WorkingMemoryRepository) key(ownerID, conversationID int64) string {
	return fmt.Sprintf("%s:%d:%d", workingMemoryPrefix, ownerID, conversationID)
}

func (r *WorkingMemoryRepository) lockKey(ownerID, conversationID int64) string {
	return fmt.Sprintf("%s:lock:%d:%d", workingMemoryPrefix, ownerID, conversationID)
}

func (r *WorkingMemoryRepository) Get(ctx context.Context, ownerID, conversationID int64) (*memory.WorkingMemory, error) {
	raw, err := r.client.Get(ctx, r.key(ownerID, conversationID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var wm memory.WorkingMemory
	if err := json.Unmarshal(raw, &wm); err != nil {
		return nil, fmt.Errorf("working memory deserialize: %w", err)
	}
	return &wm, nil
}

func (r *WorkingMemoryRepository) Save(ctx context.Context, wm *memory.WorkingMemory) error {
	wm.LastUpdated = time.Now().UTC()
	data, err := json.Marshal(wm)
	if err != nil {
		return fmt.Errorf("working memory serialize: %w", err)
	}
	return r.client.Set(ctx, r.key(wm.OwnerID, wm.ConversationID), data, r.ttl).Err()
}

func (r *WorkingMemoryRepository) Update(ctx context.Context, ownerID, conversationID int64, mutate func(*memory.WorkingMemory) error) (*memory.WorkingMemory, error) {
	if mutate == nil {
		return nil, fmt.Errorf("working memory mutate callback is required")
	}
	token := uuid.NewString()
	deadline := time.Now().Add(r.lockWait)
	for {
		locked, err := r.client.SetNX(ctx, r.lockKey(ownerID, conversationID), token, r.lockTTL).Result()
		if err != nil {
			return nil, err
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			return nil, ErrWorkingMemoryLockTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer r.releaseLock(ownerID, conversationID, token)
	wm, err := r.Get(ctx, ownerID, conversationID)
	if err != nil {
		return nil, err
	}
	if wm == nil {
		wm = &memory.WorkingMemory{OwnerID: ownerID, ConversationID: conversationID}
	}
	if err := mutate(wm); err != nil {
		return nil, err
	}
	if err := r.Save(ctx, wm); err != nil {
		return nil, err
	}
	return wm, nil
}

func (r *WorkingMemoryRepository) releaseLock(ownerID, conversationID int64, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	const compareAndDelete = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
	_, _ = r.client.Eval(ctx, compareAndDelete, []string{r.lockKey(ownerID, conversationID)}, token).Result()
}

func (r *WorkingMemoryRepository) Delete(ctx context.Context, ownerID, conversationID int64) error {
	return r.client.Del(ctx, r.key(ownerID, conversationID)).Err()
}
