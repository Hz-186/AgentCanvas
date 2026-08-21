package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"agentcanvas/internal/domain/knowledge"
)

type MySQLIngestionQueue struct {
	Repo knowledge.IngestionJobRepository
}

func NewMySQLIngestionQueue(repo knowledge.IngestionJobRepository) *MySQLIngestionQueue {
	return &MySQLIngestionQueue{Repo: repo}
}

func (q *MySQLIngestionQueue) Publish(ctx context.Context, job Job) error {
	if q == nil || q.Repo == nil {
		return fmt.Errorf("ingestion job repository is not configured")
	}
	item := &knowledge.IngestionJob{
		JobType:     job.Type,
		Status:      knowledge.IngestionJobStatusPending,
		MaxAttempts: 3,
	}
	if job.ID != "" {
		item.ID, _ = strconv.ParseInt(job.ID, 10, 64)
	}
	item.OwnerID = int64Payload(job.Payload, "owner_id")
	item.KBID = int64Payload(job.Payload, "kb_id")
	item.DocumentID = int64Payload(job.Payload, "document_id")
	return q.Repo.Create(ctx, item)
}

func (q *MySQLIngestionQueue) Claim(ctx context.Context, opts ClaimOptions) ([]Job, error) {
	if q == nil || q.Repo == nil {
		return nil, fmt.Errorf("ingestion job repository is not configured")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1
	}
	jobs := make([]Job, 0, limit)
	for len(jobs) < limit {
		item, err := q.Repo.ClaimNext(ctx, opts.WorkerID)
		if err != nil {
			if len(jobs) > 0 {
				return jobs, nil
			}
			return nil, err
		}
		jobs = append(jobs, jobFromIngestion(item))
	}
	return jobs, nil
}

func (q *MySQLIngestionQueue) Ack(ctx context.Context, jobID string) error {
	if q == nil || q.Repo == nil {
		return fmt.Errorf("ingestion job repository is not configured")
	}
	id, err := strconv.ParseInt(jobID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid ingestion job id %s", jobID)
	}
	return q.Repo.MarkCompleted(ctx, id)
}

func (q *MySQLIngestionQueue) Nack(ctx context.Context, jobID string, retryAt time.Time) error {
	if q == nil || q.Repo == nil {
		return fmt.Errorf("ingestion job repository is not configured")
	}
	id, err := strconv.ParseInt(jobID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid ingestion job id %s", jobID)
	}
	if retryable, ok := q.Repo.(knowledge.RetryableIngestionJobRepository); ok {
		_, err = retryable.MarkFailedAt(ctx, id, "job nacked by queue adapter", retryAt)
	} else {
		_, err = q.Repo.MarkFailed(ctx, id, "job nacked by queue adapter")
	}
	return err
}

func jobFromIngestion(item *knowledge.IngestionJob) Job {
	if item == nil {
		return Job{}
	}
	return Job{
		ID:          strconv.FormatInt(item.ID, 10),
		Type:        item.JobType,
		Attempts:    item.AttemptCount,
		MaxAttempts: item.MaxAttempts,
		AvailableAt: valueOrZeroTime(item.RetryAt),
		Payload: map[string]any{
			"owner_id":     item.OwnerID,
			"kb_id":        item.KBID,
			"document_id":  item.DocumentID,
			"status":       item.Status,
			"locked_by":    item.LockedBy,
			"max_attempts": item.MaxAttempts,
		},
	}
}

func valueOrZeroTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func int64Payload(payload map[string]any, key string) int64 {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		parsed, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return parsed
	}
}
