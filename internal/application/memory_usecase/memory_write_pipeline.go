package memory_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
)

const (
	writeJobClaimBatch = 1
	writeJobLease      = 2 * time.Minute
	writeJobMaxBackoff = 6 * time.Hour
	writeJobMaxAttempt = 8
)

// WriteJobRequest is the unified producer envelope. Every memory producer —
// ad_hoc, extraction, consolidation, proposal, reflection and manual — submits
// the same envelope: owner, source, idempotency key and a write payload.
type WriteJobRequest struct {
	OwnerID        int64
	Source         string
	IdempotencyKey string
	Payload        memory.WriteRequest
}

// WriteJobWriter is the transactional SQL write boundary. A successful call
// commits exactly one memory row and MUST enqueue the post-commit context
// outbox event (owner/resource/content version) atomically with that commit.
// A failed call rolls back and MUST NOT emit any outbox event.
type WriteJobWriter interface {
	Write(ctx context.Context, job *memory.MemoryWriteJob, req memory.WriteRequest) (*memory.Memory, error)
}

// WriteJobWarnings receives fail-open warnings. Enqueue failures never fail
// the calling run; they are reported here so they stay observable.
type WriteJobWarnings interface {
	EmitWarning(ctx context.Context, message string)
}

// SlogWriteWarnings reports pipeline warnings through slog.
type SlogWriteWarnings struct {
	Logger *slog.Logger
}

func (w SlogWriteWarnings) EmitWarning(ctx context.Context, message string) {
	if w.Logger != nil {
		w.Logger.WarnContext(ctx, message)
	}
}

// MemoryWritePipeline owns the unified asynchronous memory write path. The
// enqueue side is non-blocking and fail-open; the worker side claims leased
// rows, writes SQL and retries failures with backoff.
type MemoryWritePipeline struct {
	jobs        memory.MemoryWriteJobRepository
	writer      WriteJobWriter
	warnings    WriteJobWarnings
	workerID    string
	lease       time.Duration
	maxAttempts int
}

func NewMemoryWritePipeline(workerID string, jobs memory.MemoryWriteJobRepository, writer WriteJobWriter, warnings WriteJobWarnings) *MemoryWritePipeline {
	if strings.TrimSpace(workerID) == "" {
		workerID = "memory-write-worker"
	}
	return &MemoryWritePipeline{
		jobs:        jobs,
		writer:      writer,
		warnings:    warnings,
		workerID:    workerID,
		lease:       writeJobLease,
		maxAttempts: writeJobMaxAttempt,
	}
}

// Enqueue persists one idempotent memory_write_jobs row. It never waits for
// the writer or for SQL/ES work; duplicates short-circuit on the repository
// (owner_id, idempotency_key) unique key. Failure is fail-open: the caller
// keeps its successful result and a warning event is emitted.
func (p *MemoryWritePipeline) Enqueue(ctx context.Context, req WriteJobRequest) error {
	if req.OwnerID <= 0 {
		return fmt.Errorf("memory write owner is required")
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		return fmt.Errorf("memory write idempotency key is required")
	}
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return fmt.Errorf("encode memory write payload: %w", err)
	}
	job := &memory.MemoryWriteJob{
		BaseModel:      domain.BaseModel{OwnerID: req.OwnerID},
		Source:         memory.CanonicalSource(req.Source),
		IdempotencyKey: key,
		PayloadJSON:    payload,
		Status:         memory.WriteJobStatusPending,
	}
	if err := p.jobs.Create(ctx, job); err != nil {
		if p.warnings != nil {
			p.warnings.EmitWarning(ctx, fmt.Sprintf("memory write job enqueue failed: %v", err))
		}
		return err
	}
	return nil
}

// ProcessNext claims one pending/lease-expired job and runs the write step.
// SQL failures are retried under lease with backoff; a claimed job is never
// left locked after the step returns.
func (p *MemoryWritePipeline) ProcessNext(ctx context.Context) (bool, error) {
	if p == nil || p.jobs == nil {
		return false, nil
	}
	now := time.Now().UTC()
	lease := p.lease
	if lease <= 0 {
		lease = writeJobLease
	}
	claimed, err := p.jobs.ClaimPending(ctx, p.workerID, now, now.Add(lease), writeJobClaimBatch)
	if err != nil || len(claimed) == 0 {
		return false, err
	}
	job := claimed[0]
	if err := p.processClaimed(ctx, &job); err != nil {
		return true, err
	}
	return true, nil
}

func (p *MemoryWritePipeline) processClaimed(ctx context.Context, job *memory.MemoryWriteJob) error {
	var req memory.WriteRequest
	if len(job.PayloadJSON) > 0 {
		if err := json.Unmarshal(job.PayloadJSON, &req); err != nil {
			return p.failClaim(ctx, job, fmt.Errorf("decode memory write payload: %w", err))
		}
	}
	// A phase-one extraction with no signal is a legitimate no-op: the job is
	// completed without a memory insert and without touching the writer.
	if strings.TrimSpace(req.MemoryType) == "" && strings.TrimSpace(req.Content) == "" {
		return p.completeClaim(ctx, job)
	}
	if p.writer == nil {
		return p.failClaim(ctx, job, fmt.Errorf("memory write worker is not configured"))
	}
	if _, err := p.writer.Write(ctx, job, req); err != nil {
		return p.failClaim(ctx, job, err)
	}
	return p.completeClaim(ctx, job)
}

func (p *MemoryWritePipeline) completeClaim(ctx context.Context, job *memory.MemoryWriteJob) error {
	now := time.Now().UTC()
	job.Status = memory.WriteJobStatusCompleted
	job.ErrorMessage = ""
	job.CompletedAt = &now
	job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
	return p.jobs.Update(ctx, job)
}

func (p *MemoryWritePipeline) failClaim(ctx context.Context, job *memory.MemoryWriteJob, cause error) error {
	now := time.Now().UTC()
	job.ErrorMessage = cause.Error()
	job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
	maxAttempts := p.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = writeJobMaxAttempt
	}
	if job.AttemptCount >= maxAttempts {
		job.Status = memory.WriteJobStatusDeadLetter
		if err := p.jobs.Update(ctx, job); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	// Requeue under the same idempotent envelope. ClaimPending picks it up
	// again once due_at arrives; the (owner_id, idempotency_key) unique key
	// and the writer transaction keep the eventual row exactly-once.
	job.Status = memory.WriteJobStatusPending
	job.DueAt = writeJobDueTime(now, job.AttemptCount)
	if err := p.jobs.Update(ctx, job); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func writeJobRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := time.Minute * time.Duration(1<<uint(attempt-1))
	if delay > writeJobMaxBackoff {
		return writeJobMaxBackoff
	}
	return delay
}

func writeJobDueTime(now time.Time, attempt int) *time.Time {
	due := now.Add(writeJobRetryDelay(attempt))
	return &due
}

// WriteJobWorker is the small worker-loop seam exposed to cmd/worker. It
// drains claimed jobs one at a time through the pipeline step function.
type WriteJobWorker struct {
	pipeline *MemoryWritePipeline
}

func NewWriteJobWorker(pipeline *MemoryWritePipeline) *WriteJobWorker {
	return &WriteJobWorker{pipeline: pipeline}
}

func (w *WriteJobWorker) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.pipeline == nil {
		return false, nil
	}
	return w.pipeline.ProcessNext(ctx)
}

// SQLMemoryWriter adapts the canonical memory repository to the write-job
// writer contract. The repository Create transaction is the single commit
// boundary: the memory row and the post-commit context outbox event are
// persisted together, so rollback emits nothing.
type SQLMemoryWriter struct {
	memories memory.Repository
}

func NewSQLMemoryWriter(memories memory.Repository) *SQLMemoryWriter {
	return &SQLMemoryWriter{memories: memories}
}

func (w *SQLMemoryWriter) Write(ctx context.Context, job *memory.MemoryWriteJob, req memory.WriteRequest) (*memory.Memory, error) {
	if w == nil || w.memories == nil {
		return nil, fmt.Errorf("memory writer repository is not configured")
	}
	memoryType := strings.TrimSpace(req.MemoryType)
	content := strings.TrimSpace(req.Content)
	if memoryType == "" || content == "" {
		return nil, fmt.Errorf("memory_type and content are required")
	}
	scopeType, scopeID, err := memory.ResolveScope(memoryType, req.OwnerID, req.AgentID, req.ProjectID, req.ConversationID, req.ScopeType, req.ScopeID)
	if err != nil {
		return nil, err
	}
	item := &memory.Memory{
		SoftDeleteModel:      domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: req.OwnerID}},
		SourceConversationID: req.SourceConversationID,
		SourceProjectID:      req.SourceProjectID,
		ScopeType:            scopeType,
		ScopeID:              scopeID,
		MemoryType:           memoryType,
		RetentionTier:        memory.TierLongTerm,
		Title:                strings.TrimSpace(req.Title),
		Content:              content,
		Importance:           req.Importance,
		Source:               memory.CanonicalSource(req.Source),
		DeduplicationKey:     req.DeduplicationKey,
	}
	if item.Importance <= 0 {
		item.Importance = 0.5
	}
	if item.Importance > 1 {
		item.Importance = 1
	}
	// A retried claim must not create a second memory row. Without an explicit
	// deduplication key, the job idempotency key is the exactly-once guard.
	if item.DeduplicationKey == nil && job != nil {
		key := strings.TrimSpace(job.IdempotencyKey)
		item.DeduplicationKey = &key
	}
	if err := w.memories.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// AdHocWriteJobAdapter routes explicit ad-hoc memory notes through the
// unified write-job pipeline with source ad_hoc. It replaces the retired
// synchronous durable-file note writer at the runtime finalization seam.
type AdHocWriteJobAdapter struct {
	pipeline *MemoryWritePipeline
}

func NewAdHocWriteJobAdapter(pipeline *MemoryWritePipeline) *AdHocWriteJobAdapter {
	return &AdHocWriteJobAdapter{pipeline: pipeline}
}

func (a *AdHocWriteJobAdapter) AppendAdHocNote(ctx context.Context, ownerID, conversationID, runID int64, request, answer string) (string, error) {
	if !HasExplicitMemoryIntent(request) {
		return "", fmt.Errorf("explicit memory intent is required")
	}
	if ownerID <= 0 {
		return "", fmt.Errorf("durable memory owner is required")
	}
	if runID <= 0 {
		return "", fmt.Errorf("positive source run id is required")
	}
	key := fmt.Sprintf("ad_hoc:%d", runID)
	content := strings.TrimSpace(request)
	if answerText := strings.TrimSpace(answer); answerText != "" {
		content += "\n\n" + answerText
	}
	err := a.pipeline.Enqueue(ctx, WriteJobRequest{
		OwnerID:        ownerID,
		Source:         "ad_hoc",
		IdempotencyKey: key,
		Payload: memory.WriteRequest{
			OwnerID:        ownerID,
			ConversationID: conversationID,
			RunID:          runID,
			Source:         "ad_hoc",
			MemoryType:     memory.TypeArchival,
			Content:        content,
		},
	})
	if err != nil {
		// Fail-open: the calling run already has a successful final answer.
		// The warning was emitted by the pipeline; the error stays observable
		// for the runtime warning event.
		return "", err
	}
	return "memory-write-job:" + key, nil
}

var _ memory.AdHocWriter = (*AdHocWriteJobAdapter)(nil)
var _ WriteJobWriter = (*SQLMemoryWriter)(nil)
