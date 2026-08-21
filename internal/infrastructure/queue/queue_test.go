package queue

import (
	"context"
	"testing"
	"time"
)

func TestMemoryQueuePublishClaimAck(t *testing.T) {
	q := NewMemoryQueue()
	if err := q.Publish(context.Background(), Job{ID: "job1", Type: "ingest"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "w1", Limit: 1})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job1" || jobs[0].Attempts != 1 {
		t.Fatalf("unexpected claimed jobs: %+v", jobs)
	}
	if err := q.Ack(context.Background(), "job1"); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	jobs, err = q.Claim(context.Background(), ClaimOptions{WorkerID: "w1", Limit: 1})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs after ack, got %+v", jobs)
	}
}

func TestMemoryQueueNackDelaysRetry(t *testing.T) {
	q := NewMemoryQueue()
	now := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	if err := q.Publish(context.Background(), Job{ID: "job1", Type: "ingest"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	jobs, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "w1", Limit: 1, Now: now})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Claim() = %+v, %v", jobs, err)
	}
	if err := q.Nack(context.Background(), "job1", now.Add(time.Minute)); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	jobs, err = q.Claim(context.Background(), ClaimOptions{WorkerID: "w1", Limit: 1, Now: now.Add(30 * time.Second)})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected delayed retry to stay pending, got %+v", jobs)
	}
	jobs, err = q.Claim(context.Background(), ClaimOptions{WorkerID: "w1", Limit: 1, Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].Attempts != 2 {
		t.Fatalf("expected retry claim, got %+v", jobs)
	}
}

func TestMemoryQueueMovesExhaustedJobToDeadJobs(t *testing.T) {
	q := NewMemoryQueue()
	if err := q.Publish(context.Background(), Job{ID: "job1", Type: "ingest", MaxAttempts: 1}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	jobs, err := q.Claim(context.Background(), ClaimOptions{Limit: 1})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Claim() = %+v, %v", jobs, err)
	}
	if err := q.Nack(context.Background(), "job1", time.Now()); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	jobs, err = q.Claim(context.Background(), ClaimOptions{Limit: 1})
	if err != nil || len(jobs) != 0 {
		t.Fatalf("exhausted job was requeued: %+v, %v", jobs, err)
	}
	dead := q.DeadJobs()
	if len(dead) != 1 || dead[0].ID != "job1" || dead[0].Attempts != 1 {
		t.Fatalf("dead jobs = %+v", dead)
	}
}
