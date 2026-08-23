package queue

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const JobSchemaVersion = 1

type Job struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload,omitempty"`
	AttemptCount  int            `json:"attempt_count"`
	MaxAttempts   int            `json:"max_attempts,omitempty"`
	AvailableAt   time.Time      `json:"available_at,omitempty"`
}

type ClaimOptions struct {
	WorkerID string
	Limit    int
	Now      time.Time
}

type JobQueue interface {
	Publish(ctx context.Context, job Job) error
	Claim(ctx context.Context, opts ClaimOptions) ([]Job, error)
	Ack(ctx context.Context, jobID string) error
	Nack(ctx context.Context, jobID string, retryAt time.Time) error
}

// MemoryQueue is only suitable for unit tests and local in-process experiments.
// Production ingestion should use MySQL, Redis Stream, or NATS JetStream.
type MemoryQueue struct {
	mu       sync.Mutex
	pending  []Job
	claimed  map[string]Job
	deadJobs map[string]Job
}

func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{claimed: map[string]Job{}, deadJobs: map[string]Job{}}
}

func (q *MemoryQueue) Publish(ctx context.Context, job Job) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if job.ID == "" || job.Type == "" {
		return fmt.Errorf("job id and type are required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, job)
	return nil
}

func (q *MemoryQueue) Claim(ctx context.Context, opts ClaimOptions) ([]Job, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if opts.Limit <= 0 {
		opts.Limit = 1
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	claimed := make([]Job, 0, opts.Limit)
	remaining := q.pending[:0]
	for _, job := range q.pending {
		if len(claimed) >= opts.Limit || (!job.AvailableAt.IsZero() && job.AvailableAt.After(now)) {
			remaining = append(remaining, job)
			continue
		}
		job.AttemptCount++
		q.claimed[job.ID] = job
		claimed = append(claimed, job)
	}
	q.pending = remaining
	return claimed, nil
}

func (q *MemoryQueue) Ack(ctx context.Context, jobID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.claimed[jobID]; !ok {
		return fmt.Errorf("claimed job %s not found", jobID)
	}
	delete(q.claimed, jobID)
	return nil
}

func (q *MemoryQueue) Nack(ctx context.Context, jobID string, retryAt time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.claimed[jobID]
	if !ok {
		return fmt.Errorf("claimed job %s not found", jobID)
	}
	delete(q.claimed, jobID)
	job.AvailableAt = retryAt
	if job.MaxAttempts > 0 && job.AttemptCount >= job.MaxAttempts {
		q.deadJobs[job.ID] = job
		return nil
	}
	q.pending = append(q.pending, job)
	return nil
}

func (q *MemoryQueue) DeadJobs() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	dead := make([]Job, 0, len(q.deadJobs))
	for _, job := range q.deadJobs {
		dead = append(dead, job)
	}
	return dead
}
