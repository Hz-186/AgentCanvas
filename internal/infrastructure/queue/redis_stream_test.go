package queue

import (
	"context"
	"testing"
	"time"
)

func TestRedisStreamQueuePublishAndClaim(t *testing.T) {
	fake := &fakeRedisStreamClient{}
	q := NewRedisStreamQueueWithClient(fake, "jobs", "workers", "fallback")
	availableAt := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	if err := q.Publish(context.Background(), Job{ID: "biz-1", Type: "ingest", Attempts: 2, AvailableAt: availableAt, Payload: map[string]any{"document_id": 7}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if fake.ensureStream != "jobs" || fake.ensureGroup != "workers" {
		t.Fatalf("expected consumer group to be ensured, got stream=%s group=%s", fake.ensureStream, fake.ensureGroup)
	}
	if len(fake.messages) != 1 || fake.messages[0].Values["type"] != "ingest" || fake.messages[0].Values["job_id"] != "biz-1" {
		t.Fatalf("unexpected stream message: %+v", fake.messages)
	}
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "worker-1", Limit: 3})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if fake.consumer != "worker-1" || fake.count != 3 {
		t.Fatalf("expected read group options to be passed, got consumer=%s count=%d", fake.consumer, fake.count)
	}
	if len(jobs) != 1 || jobs[0].ID != "1-0" || jobs[0].Type != "ingest" || jobs[0].Attempts != 3 || jobs[0].Payload["document_id"] != float64(7) || jobs[0].Payload["job_id"] != "biz-1" {
		t.Fatalf("unexpected claimed jobs: %+v", jobs)
	}
}

func TestRedisStreamQueueAckAndNack(t *testing.T) {
	fake := &fakeRedisStreamClient{messages: []RedisStreamMessage{{ID: "1-0", Values: map[string]any{"type": "ingest", "payload": `{}`}}}}
	q := NewRedisStreamQueueWithClient(fake, "jobs", "workers", "fallback")
	if err := q.Ack(context.Background(), "1-0"); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	if len(fake.acked) != 1 || fake.acked[0] != "1-0" {
		t.Fatalf("expected acked stream id, got %+v", fake.acked)
	}
	if err := q.Nack(context.Background(), "2-0", time.Now()); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if len(fake.acked) != 2 || fake.acked[1] != "2-0" {
		t.Fatalf("expected nack to ack failed delivery, got %+v", fake.acked)
	}
	if len(fake.messages) != 2 || fake.messages[1].Values["type"] != "retry" {
		t.Fatalf("expected nack to publish retry message, got %+v", fake.messages)
	}
}

func TestRedisStreamQueueDoesNotRepublishExhaustedJob(t *testing.T) {
	fake := &fakeRedisStreamClient{messages: []RedisStreamMessage{{ID: "1-0", Values: map[string]any{
		"job_id": "biz-1", "type": "ingest", "payload": `{}`, "attempts": "1", "max_attempts": "2",
	}}}}
	q := NewRedisStreamQueueWithClient(fake, "jobs", "workers", "fallback")
	jobs, err := q.Claim(context.Background(), ClaimOptions{Limit: 1})
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 2 {
		t.Fatalf("Claim() = %+v, %v", jobs, err)
	}
	if err := q.Nack(context.Background(), "1-0", time.Now()); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if len(fake.messages) != 1 || len(fake.acked) != 1 || fake.acked[0] != "1-0" {
		t.Fatalf("exhausted job was not terminally acked: messages=%+v acked=%+v", fake.messages, fake.acked)
	}
}

func TestRedisStreamQueueReclaimsCrashedConsumerMessage(t *testing.T) {
	fake := &fakeRedisStreamClient{reclaimed: []RedisStreamMessage{{ID: "9-0", Values: map[string]any{"job_id": "biz-9", "type": "ingest", "payload": `{}`}}}}
	q := NewRedisStreamQueueWithClient(fake, "jobs", "workers", "fallback")
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "worker-2", Limit: 1})
	if err != nil || len(jobs) != 1 || jobs[0].ID != "9-0" || fake.autoClaimConsumer != "worker-2" {
		t.Fatalf("pending message was not reclaimed: jobs=%+v consumer=%s err=%v", jobs, fake.autoClaimConsumer, err)
	}
}

func TestRedisStreamQueueLeavesFutureRetryPending(t *testing.T) {
	future := time.Now().Add(time.Hour)
	fake := &fakeRedisStreamClient{reclaimed: []RedisStreamMessage{{ID: "9-0", Values: map[string]any{
		"job_id": "biz-9", "type": "ingest", "payload": `{}`, "available_at": future.Format(time.RFC3339Nano),
	}}}}
	q := NewRedisStreamQueueWithClient(fake, "jobs", "workers", "fallback")
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "worker-2", Limit: 1, Now: future.Add(-time.Minute)})
	if err != nil || len(jobs) != 0 || len(fake.acked) != 0 {
		t.Fatalf("future retry must remain pending: jobs=%+v acked=%v err=%v", jobs, fake.acked, err)
	}
}

type fakeRedisStreamClient struct {
	ensureStream      string
	ensureGroup       string
	messages          []RedisStreamMessage
	acked             []string
	consumer          string
	count             int64
	reclaimed         []RedisStreamMessage
	autoClaimConsumer string
}

func (c *fakeRedisStreamClient) EnsureGroup(_ context.Context, stream, group string) error {
	c.ensureStream = stream
	c.ensureGroup = group
	return nil
}

func (c *fakeRedisStreamClient) Add(_ context.Context, stream string, values map[string]any) (string, error) {
	id := "1-0"
	if len(c.messages) > 0 {
		id = "2-0"
	}
	c.messages = append(c.messages, RedisStreamMessage{ID: id, Values: values})
	return id, nil
}

func (c *fakeRedisStreamClient) ReadGroup(_ context.Context, stream, group, consumer string, count int64, block time.Duration) ([]RedisStreamMessage, error) {
	c.consumer = consumer
	c.count = count
	return append([]RedisStreamMessage(nil), c.messages...), nil
}

func (c *fakeRedisStreamClient) AutoClaim(_ context.Context, stream, group, consumer string, minIdle time.Duration, count int64) ([]RedisStreamMessage, error) {
	c.autoClaimConsumer = consumer
	return append([]RedisStreamMessage(nil), c.reclaimed...), nil
}

func (c *fakeRedisStreamClient) Ack(_ context.Context, stream, group string, ids ...string) (int64, error) {
	c.acked = append(c.acked, ids...)
	return int64(len(ids)), nil
}
