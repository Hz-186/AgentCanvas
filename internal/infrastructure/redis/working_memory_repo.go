package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain/memory"

	"github.com/redis/go-redis/v9"
)

const (
	workingMemoryPrefix = "wm"
	workingMemoryTTL    = 24 * time.Hour
)

type WorkingMemoryRepository struct {
	client *redis.Client
}

func NewWorkingMemoryRepository(client *redis.Client) *WorkingMemoryRepository {
	return &WorkingMemoryRepository{client: client}
}

func (r *WorkingMemoryRepository) key(ownerID, conversationID int64) string {
	return fmt.Sprintf("%s:%d:%d", workingMemoryPrefix, ownerID, conversationID)
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
	return r.client.Set(ctx, r.key(wm.OwnerID, wm.ConversationID), data, workingMemoryTTL).Err()
}

func (r *WorkingMemoryRepository) Delete(ctx context.Context, ownerID, conversationID int64) error {
	return r.client.Del(ctx, r.key(ownerID, conversationID)).Err()
}
