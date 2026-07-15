package reflection_usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	mathrand "math/rand"
	"sync"
	"time"

	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/pkg/config"

	"gorm.io/gorm"
)

type QueueRuntime struct {
	Worker    Worker
	Jobs      reflection.ReliableJobRepository
	Outbox    reflection.OutboxRepository
	Transport reflection.Transport
	Config    config.ReflectionQueueConfig
	WorkerID  string
	Logger    *slog.Logger
}

func (r *QueueRuntime) Run(ctx context.Context) error {
	if r.Jobs == nil || r.Outbox == nil || r.Transport == nil {
		return fmt.Errorf("reflection queue runtime is not configured")
	}
	if r.Logger == nil {
		r.Logger = slog.Default()
	}
	if _, err := r.Jobs.BackfillPendingDispatches(ctx, 1000); err != nil {
		r.Logger.Error("reflection pending dispatch backfill failed", "error", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.runOutbox(ctx)
	}()
	for i := 0; i < r.Config.Concurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			r.runConsumer(ctx, fmt.Sprintf("%s-%d", r.WorkerID, index))
		}(i)
	}
	<-ctx.Done()
	wg.Wait()
	return r.Transport.Drain()
}

func (r *QueueRuntime) runOutbox(ctx context.Context) {
	poll := time.Duration(r.Config.OutboxPollMilliseconds) * time.Millisecond
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.dispatchOutbox(ctx)
		case <-cleanup.C:
			r.cleanupOutbox(ctx)
		}
	}
}

func (r *QueueRuntime) dispatchOutbox(ctx context.Context) {
	items, err := r.Outbox.ClaimOutbox(ctx, r.WorkerID, r.Config.OutboxBatchSize, time.Minute)
	if err != nil {
		r.Logger.Error("reflection outbox claim failed", "error", err)
		return
	}
	for _, item := range items {
		started := time.Now()
		if err := r.Transport.PublishOutbox(ctx, item); err != nil {
			observability.ReflectionSystemMetrics.RecordPublishLatency(time.Since(started))
			next := time.Now().UTC().Add(outboxRetryDelay(item.AttemptCount + 1))
			_ = r.Outbox.MarkOutboxFailed(context.WithoutCancel(ctx), item.ID, r.WorkerID, err, next)
			observability.ReflectionSystemMetrics.RecordOutboxPublishFailure()
			r.Logger.Error("reflection outbox publish failed", "job_id", item.JobID, "event_id", item.EventID,
				"dispatch_seq", item.DispatchSeq, "attempt", item.AttemptCount+1, "error", err)
			continue
		}
		observability.ReflectionSystemMetrics.RecordPublishLatency(time.Since(started))
		if err := r.Outbox.MarkOutboxPublished(context.WithoutCancel(ctx), item.ID, r.WorkerID); err != nil {
			r.Logger.Error("reflection outbox completion failed", "job_id", item.JobID, "event_id", item.EventID, "error", err)
			continue
		}
		observability.ReflectionSystemMetrics.RecordOutboxPublished()
	}
}

func (r *QueueRuntime) cleanupOutbox(ctx context.Context) {
	for {
		deleted, err := r.Outbox.DeletePublishedOutboxBefore(ctx, time.Now().UTC().Add(-7*24*time.Hour), 1000)
		if err != nil {
			r.Logger.Error("reflection outbox cleanup failed", "error", err)
			return
		}
		if deleted < 1000 {
			return
		}
	}
}

func (r *QueueRuntime) runConsumer(ctx context.Context, workerID string) {
	for ctx.Err() == nil {
		deliveries, err := r.Transport.Fetch(ctx, 1)
		if err != nil {
			if ctx.Err() == nil {
				r.Logger.Error("reflection message fetch failed", "worker_id", workerID, "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for _, delivery := range deliveries {
			r.handleDelivery(ctx, workerID, delivery)
		}
	}
}

func (r *QueueRuntime) handleDelivery(ctx context.Context, workerID string, delivery reflection.Delivery) {
	envelope := delivery.Envelope()
	if err := delivery.ValidationError(); err != nil {
		if publishErr := r.Transport.PublishDLQ(context.WithoutCancel(ctx), envelope, err.Error()); publishErr != nil {
			_ = delivery.Nak(context.WithoutCancel(ctx), time.Minute)
			return
		}
		_ = delivery.Term(context.WithoutCancel(ctx))
		observability.ReflectionSystemMetrics.RecordDLQJob()
		return
	}
	lockToken, err := queueLockToken()
	if err != nil {
		_ = delivery.Nak(context.WithoutCancel(ctx), time.Minute)
		return
	}
	leaseUntil := time.Now().UTC().Add(time.Duration(r.Config.LeaseSeconds) * time.Second)
	job, state, err := r.Jobs.ClaimByID(ctx, envelope.JobID, workerID, lockToken, leaseUntil)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if publishErr := r.Transport.PublishDLQ(context.WithoutCancel(ctx), envelope, "reflection job not found"); publishErr == nil {
			_ = delivery.Term(context.WithoutCancel(ctx))
		} else {
			_ = delivery.Nak(context.WithoutCancel(ctx), time.Minute)
		}
		return
	}
	if err != nil {
		_ = delivery.Nak(context.WithoutCancel(ctx), 10*time.Second)
		return
	}
	switch state {
	case reflection.ClaimTerminal:
		_ = delivery.Ack(context.WithoutCancel(ctx))
		return
	case reflection.ClaimBusy:
		delay := 5 * time.Second
		if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(time.Now()) {
			delay = time.Until(*job.LeaseExpiresAt) + time.Second
		} else if job.RetryAt != nil && job.RetryAt.After(time.Now()) {
			delay = time.Until(*job.RetryAt) + time.Second
		}
		_ = delivery.Nak(context.WithoutCancel(ctx), delay)
		return
	}

	processCtx, cancel := context.WithCancel(context.Background())
	graceDone := make(chan struct{})
	go func() {
		select {
		case <-graceDone:
		case <-ctx.Done():
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			select {
			case <-graceDone:
			case <-timer.C:
				cancel()
			}
		}
	}()
	processingStarted := time.Now()
	defer func() {
		observability.ReflectionSystemMetrics.RecordProcessingLatency(time.Since(processingStarted))
	}()
	heartbeatDone := make(chan error, 1)
	go r.heartbeat(processCtx, cancel, job, delivery, heartbeatDone)
	items, processErr := r.Worker.analyze(processCtx, job)
	close(graceDone)
	cancel()
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil && processErr == nil {
		processErr = heartbeatErr
	}
	opCtx, opCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer opCancel()
	if processErr != nil {
		if err := r.Worker.scheduleFailure(opCtx, job, processErr); err != nil {
			if errors.Is(err, reflection.ErrLeaseLost) {
				observability.ReflectionSystemMetrics.RecordLeaseConflict()
			}
			_ = delivery.Nak(opCtx, 10*time.Second)
			return
		}
		_ = delivery.Ack(opCtx)
		return
	}
	if err := r.Jobs.CommitResult(opCtx, job.ID, job.LockToken, items); err != nil {
		if errors.Is(err, reflection.ErrLeaseLost) {
			observability.ReflectionSystemMetrics.RecordLeaseConflict()
		}
		_ = delivery.Nak(opCtx, 10*time.Second)
		return
	}
	observability.ReflectionSystemMetrics.RecordJobCompleted()
	if err := delivery.Ack(opCtx); err != nil {
		r.Logger.Error("reflection message ack failed", "job_id", job.ID, "event_id", envelope.EventID, "worker_id", workerID, "error", err)
	}
}

func (r *QueueRuntime) heartbeat(ctx context.Context, cancel context.CancelFunc, job *reflection.Job, delivery reflection.Delivery, done chan<- error) {
	interval := time.Duration(r.Config.HeartbeatSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	natsFailures := 0
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			if err := delivery.InProgress(ctx); err != nil {
				natsFailures++
				observability.ReflectionSystemMetrics.RecordHeartbeatFailure()
				if natsFailures >= 2 {
					cancel()
					done <- fmt.Errorf("reflection nats heartbeat failed: %w", err)
					return
				}
			} else {
				natsFailures = 0
			}
			leaseUntil := time.Now().UTC().Add(time.Duration(r.Config.LeaseSeconds) * time.Second)
			if err := r.Jobs.RenewLease(ctx, job.ID, job.LockToken, leaseUntil); err != nil {
				if errors.Is(err, reflection.ErrLeaseLost) {
					observability.ReflectionSystemMetrics.RecordLeaseConflict()
				}
				cancel()
				done <- err
				return
			}
		}
	}
}

func queueLockToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func outboxRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	seconds := math.Min(math.Pow(2, float64(attempt-1)), 300)
	jitter := .8 + mathrand.Float64()*.4
	return time.Duration(seconds*jitter) * time.Second
}
