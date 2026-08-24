package queue

import (
	"agentcanvas/internal/domain"
	"context"
	"errors"
	"testing"
	"time"

	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

func TestMySQLIngestionQueuePublishesClaimsAndAcks(t *testing.T) {
	repo := &fakeIngestionJobRepo{}
	q := NewMySQLIngestionQueue(repo)
	if err := q.Publish(context.Background(), Job{ID: "10", Type: knowledge.IngestionJobTypeDocument, Payload: map[string]any{"owner_id": int64(1), "knowledge_base_id": int64(2), "document_id": int64(3)}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "worker-1", Limit: 1})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "10" || jobs[0].Payload["document_id"] != int64(3) {
		t.Fatalf("unexpected claimed job: %+v", jobs)
	}
	if err := q.Ack(context.Background(), "10"); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	if !repo.completed[10] {
		t.Fatalf("expected job marked completed")
	}
}

func TestMySQLIngestionQueueNackMarksFailed(t *testing.T) {
	repo := &fakeIngestionJobRepo{items: []*knowledge.IngestionJob{{BaseModel: domain.BaseModel{ID: 11, OwnerID: 1}, KnowledgeBaseID: 2, DocumentID: 3, JobType: knowledge.IngestionJobTypeDocument}}}
	q := NewMySQLIngestionQueue(repo)
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "worker-1", Limit: 1})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Claim() = %+v, %v", jobs, err)
	}
	retryAt := time.Now().Add(time.Hour)
	if err := q.Nack(context.Background(), "11", retryAt); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if !repo.failed[11] {
		t.Fatalf("expected job marked failed")
	}
	if repo.retryAt[11].IsZero() || !repo.retryAt[11].Equal(retryAt) {
		t.Fatalf("retry time was not persisted: %v", repo.retryAt[11])
	}
}

type fakeIngestionJobRepo struct {
	items     []*knowledge.IngestionJob
	completed map[int64]bool
	failed    map[int64]bool
	retryAt   map[int64]time.Time
}

func (r *fakeIngestionJobRepo) Create(_ context.Context, job *knowledge.IngestionJob) error {
	r.items = append(r.items, job)
	return nil
}

func (r *fakeIngestionJobRepo) FindByID(context.Context, int64, int64) (*knowledge.IngestionJob, error) {
	return nil, errors.New("not implemented")
}

func (r *fakeIngestionJobRepo) ClaimNext(_ context.Context, workerID string) (*knowledge.IngestionJob, error) {
	if len(r.items) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	job := r.items[0]
	r.items = r.items[1:]
	job.LockedBy = workerID
	job.AttemptCount++
	return job, nil
}

func (r *fakeIngestionJobRepo) ClaimByID(_ context.Context, ownerID, id int64, workerID string) (*knowledge.IngestionJob, bool, error) {
	for _, job := range r.items {
		if job.OwnerID == ownerID && job.ID == id {
			job.LockedBy = workerID
			job.AttemptCount++
			return job, true, nil
		}
	}
	return nil, false, gorm.ErrRecordNotFound
}

func (r *fakeIngestionJobRepo) MarkCompleted(_ context.Context, id int64) error {
	if r.completed == nil {
		r.completed = map[int64]bool{}
	}
	r.completed[id] = true
	return nil
}

func (r *fakeIngestionJobRepo) MarkFailed(_ context.Context, id int64, message string) (bool, error) {
	if r.failed == nil {
		r.failed = map[int64]bool{}
	}
	r.failed[id] = true
	return true, nil
}

func (r *fakeIngestionJobRepo) MarkFailedAt(_ context.Context, id int64, _ string, retryAt time.Time) (bool, error) {
	if r.retryAt == nil {
		r.retryAt = map[int64]time.Time{}
	}
	r.retryAt[id] = retryAt
	if r.failed == nil {
		r.failed = map[int64]bool{}
	}
	r.failed[id] = true
	return true, nil
}

func (r *fakeIngestionJobRepo) MarkFailedOwnedAt(_ context.Context, _ int64, _ string, _ string, _ time.Time) (bool, error) {
	return true, nil
}
