package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type RedisStreamMessage struct {
	ID     string
	Values map[string]any
}

type RedisStreamClient interface {
	EnsureGroup(ctx context.Context, stream, group string) error
	Add(ctx context.Context, stream string, values map[string]any) (string, error)
	ReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]RedisStreamMessage, error)
	AutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int64) ([]RedisStreamMessage, error)
	Ack(ctx context.Context, stream, group string, ids ...string) (int64, error)
}

type GoRedisStreamClient struct {
	Client *goredis.Client
}

func (c GoRedisStreamClient) EnsureGroup(ctx context.Context, stream, group string) error {
	if c.Client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	err := c.Client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (c GoRedisStreamClient) Add(ctx context.Context, stream string, values map[string]any) (string, error) {
	if c.Client == nil {
		return "", fmt.Errorf("redis client is not configured")
	}
	return c.Client.XAdd(ctx, &goredis.XAddArgs{Stream: stream, Values: values}).Result()
}

func (c GoRedisStreamClient) ReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]RedisStreamMessage, error) {
	if c.Client == nil {
		return nil, fmt.Errorf("redis client is not configured")
	}
	items, err := c.Client.XReadGroup(ctx, &goredis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: count, Block: block}).Result()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, err
	}
	out := make([]RedisStreamMessage, 0)
	for _, stream := range items {
		for _, message := range stream.Messages {
			out = append(out, RedisStreamMessage{ID: message.ID, Values: message.Values})
		}
	}
	return out, nil
}

func (c GoRedisStreamClient) AutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int64) ([]RedisStreamMessage, error) {
	if c.Client == nil {
		return nil, fmt.Errorf("redis client is not configured")
	}
	items, _, err := c.Client.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream: stream, Group: group, Consumer: consumer, MinIdle: minIdle, Start: "0-0", Count: count,
	}).Result()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, err
	}
	out := make([]RedisStreamMessage, 0, len(items))
	for _, message := range items {
		out = append(out, RedisStreamMessage{ID: message.ID, Values: message.Values})
	}
	return out, nil
}

func (c GoRedisStreamClient) Ack(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	if c.Client == nil {
		return 0, fmt.Errorf("redis client is not configured")
	}
	return c.Client.XAck(ctx, stream, group, ids...).Result()
}

type RedisStreamQueue struct {
	Client    RedisStreamClient
	Stream    string
	Group     string
	Consumer  string
	Block     time.Duration
	ClaimIdle time.Duration
	mu        sync.Mutex
	inflight  map[string]Job
}

func NewRedisStreamQueue(client *goredis.Client, stream, group, consumer string) *RedisStreamQueue {
	return NewRedisStreamQueueWithClient(GoRedisStreamClient{Client: client}, stream, group, consumer)
}

func NewRedisStreamQueueWithClient(client RedisStreamClient, stream, group, consumer string) *RedisStreamQueue {
	if stream == "" {
		stream = "agentcanvas:jobs"
	}
	if group == "" {
		group = "agentcanvas-workers"
	}
	if consumer == "" {
		consumer = "worker"
	}
	return &RedisStreamQueue{Client: client, Stream: stream, Group: group, Consumer: consumer, Block: time.Second, ClaimIdle: time.Minute, inflight: map[string]Job{}}
}

func (q *RedisStreamQueue) Publish(ctx context.Context, job Job) error {
	if err := q.ensure(ctx); err != nil {
		return err
	}
	values, err := redisValuesFromJob(job)
	if err != nil {
		return err
	}
	_, err = q.Client.Add(ctx, q.Stream, values)
	return err
}

func (q *RedisStreamQueue) Claim(ctx context.Context, opts ClaimOptions) ([]Job, error) {
	if err := q.ensure(ctx); err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1
	}
	consumer := opts.WorkerID
	if consumer == "" {
		consumer = q.Consumer
	}
	messages, err := q.Client.AutoClaim(ctx, q.Stream, q.Group, consumer, q.ClaimIdle, int64(limit))
	if err != nil {
		return nil, err
	}
	if len(messages) < limit {
		fresh, readErr := q.Client.ReadGroup(ctx, q.Stream, q.Group, consumer, int64(limit-len(messages)), q.Block)
		if readErr != nil {
			return nil, readErr
		}
		messages = append(messages, fresh...)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	jobs := make([]Job, 0, len(messages))
	for _, message := range messages {
		job, err := jobFromRedisValues(message.ID, message.Values)
		if err != nil {
			_, ackErr := q.Client.Ack(ctx, q.Stream, q.Group, message.ID)
			return nil, errors.Join(err, ackErr)
		}
		if !job.AvailableAt.IsZero() && job.AvailableAt.After(now) {
			continue
		}
		job.Attempts++
		jobs = append(jobs, job)
		q.mu.Lock()
		q.inflight[job.ID] = job
		q.mu.Unlock()
	}
	return jobs, nil
}

func (q *RedisStreamQueue) Ack(ctx context.Context, jobID string) error {
	if err := q.ensure(ctx); err != nil {
		return err
	}
	_, err := q.Client.Ack(ctx, q.Stream, q.Group, jobID)
	q.mu.Lock()
	delete(q.inflight, jobID)
	q.mu.Unlock()
	return err
}

func (q *RedisStreamQueue) Nack(ctx context.Context, jobID string, retryAt time.Time) error {
	if err := q.ensure(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	job, ok := q.inflight[jobID]
	delete(q.inflight, jobID)
	q.mu.Unlock()
	_, err := q.Client.Ack(ctx, q.Stream, q.Group, jobID)
	if err != nil {
		return err
	}
	if !ok {
		return q.Publish(ctx, Job{ID: jobID, Type: "retry", AvailableAt: retryAt, Payload: map[string]any{"retry_of": jobID}})
	}
	job.AvailableAt = retryAt
	if job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
		return nil
	}
	return q.Publish(ctx, job)
}

func (q *RedisStreamQueue) ensure(ctx context.Context) error {
	if q == nil || q.Client == nil {
		return fmt.Errorf("redis stream queue is not configured")
	}
	return q.Client.EnsureGroup(ctx, q.Stream, q.Group)
}

func redisValuesFromJob(job Job) (map[string]any, error) {
	if job.Type == "" {
		return nil, fmt.Errorf("job type is required")
	}
	payload, err := json.Marshal(job.Payload)
	if err != nil {
		return nil, err
	}
	values := map[string]any{
		"job_id":       job.ID,
		"type":         job.Type,
		"payload":      string(payload),
		"attempts":     strconv.Itoa(job.Attempts),
		"max_attempts": strconv.Itoa(job.MaxAttempts),
		"available_at": job.AvailableAt.Format(time.RFC3339Nano),
	}
	return values, nil
}

func jobFromRedisValues(messageID string, values map[string]any) (Job, error) {
	job := Job{ID: messageID, Type: fmt.Sprint(values["type"])}
	if rawID := fmt.Sprint(values["job_id"]); rawID != "" && rawID != "<nil>" {
		if job.Payload == nil {
			job.Payload = map[string]any{}
		}
		job.Payload["job_id"] = rawID
	}
	if attempts, err := strconv.Atoi(fmt.Sprint(values["attempts"])); err == nil {
		job.Attempts = attempts
	}
	if maxAttempts, err := strconv.Atoi(fmt.Sprint(values["max_attempts"])); err == nil {
		job.MaxAttempts = maxAttempts
	}
	if rawAvailable := fmt.Sprint(values["available_at"]); rawAvailable != "" && rawAvailable != "<nil>" {
		if parsed, err := time.Parse(time.RFC3339Nano, rawAvailable); err == nil {
			job.AvailableAt = parsed
		}
	}
	if rawPayload := fmt.Sprint(values["payload"]); rawPayload != "" && rawPayload != "<nil>" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {
			return Job{}, err
		}
		if job.Payload == nil {
			job.Payload = payload
		} else {
			for key, value := range payload {
				job.Payload[key] = value
			}
		}
	}
	return job, nil
}
