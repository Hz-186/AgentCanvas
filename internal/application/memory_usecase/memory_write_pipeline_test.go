package memory_usecase

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"
)

// fakeWriteJobRepo is an in-memory MemoryWriteJobRepository with a steerable
// clock so lease/retry timing is deterministic without MySQL.
type fakeWriteJobRepo struct {
	mu           sync.Mutex
	jobs         map[int64]*memory.MemoryWriteJob
	byKey        map[string]int64
	nextID       int64
	now          time.Time
	lease        time.Duration
	createdCalls int
	failCreate   error
}

func newFakeWriteJobRepo() *fakeWriteJobRepo {
	return &fakeWriteJobRepo{
		jobs:   map[int64]*memory.MemoryWriteJob{},
		byKey:  map[string]int64{},
		nextID: 1,
		now:    time.Now().UTC(),
		lease:  time.Minute,
	}
}

func (r *fakeWriteJobRepo) key(ownerID int64, key string) string {
	return strconv.FormatInt(ownerID, 10) + ":" + key
}

func (r *fakeWriteJobRepo) Create(ctx context.Context, job *memory.MemoryWriteJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createdCalls++
	if r.failCreate != nil {
		return r.failCreate
	}
	if existingID, ok := r.byKey[r.key(job.OwnerID, job.IdempotencyKey)]; ok {
		*job = *r.jobs[existingID]
		return nil
	}
	job.ID = r.nextID
	r.nextID++
	if job.Status == "" {
		job.Status = memory.WriteJobStatusPending
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = r.now
	}
	clone := *job
	r.jobs[job.ID] = &clone
	r.byKey[r.key(job.OwnerID, job.IdempotencyKey)] = job.ID
	return nil
}

func (r *fakeWriteJobRepo) FindByIdempotencyKey(ctx context.Context, ownerID int64, key string) (*memory.MemoryWriteJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.byKey[r.key(ownerID, key)]; ok {
		clone := *r.jobs[id]
		return &clone, nil
	}
	return nil, errNotFound
}

func (r *fakeWriteJobRepo) ClaimPending(ctx context.Context, workerID string, now time.Time, leaseUntil time.Time, limit int) ([]memory.MemoryWriteJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	claimed := make([]memory.MemoryWriteJob, 0, limit)
	ids := make([]int64, 0, len(r.jobs))
	for id := range r.jobs {
		ids = append(ids, id)
	}
	sortInt64s(ids)
	for _, id := range ids {
		if len(claimed) >= limit {
			break
		}
		job := r.jobs[id]
		eligible := job.Status == memory.WriteJobStatusPending && (job.DueAt == nil || !job.DueAt.After(r.now))
		if job.Status == memory.WriteJobStatusRunning && job.LeaseExpiresAt != nil {
			eligible = !job.LeaseExpiresAt.After(r.now)
		}
		if !eligible {
			continue
		}
		job.Status = memory.WriteJobStatusRunning
		job.AttemptCount++
		job.LockedBy = workerID
		job.LockedAt = &r.now
		job.LeaseExpiresAt = &leaseUntil
		claimed = append(claimed, *job)
	}
	return claimed, nil
}

func (r *fakeWriteJobRepo) Update(ctx context.Context, job *memory.MemoryWriteJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *job
	r.jobs[job.ID] = &clone
	return nil
}

func (r *fakeWriteJobRepo) advance(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

func (r *fakeWriteJobRepo) countJobs() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

func (r *fakeWriteJobRepo) jobByKey(ownerID int64, key string) (*memory.MemoryWriteJob, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byKey[r.key(ownerID, key)]
	if !ok {
		return nil, false
	}
	clone := *r.jobs[id]
	return &clone, true
}

func sortInt64s(values []int64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// fakeOutboxEntry captures the post-commit context outbox event fields that
// the pipeline must deliver exactly once per successful SQL commit.
type fakeOutboxEntry struct {
	OwnerID      int64
	ResourceType string
	ResourceID   string
	Content      string
	ContentHash  string
}

// fakeWriteJobWriter simulates the transactional SQL memory writer. Successful
// commits record the exact owner/resource/content version in outboxEntries;
// failed transactions (rollback) record nothing.
type fakeWriteJobWriter struct {
	mu             sync.Mutex
	calls          int
	successfulRows int
	failFirst      bool
	failedOnce     bool
	outboxEntries  []fakeOutboxEntry
	block          chan struct{}
	entered        chan struct{}
}

func (w *fakeWriteJobWriter) Write(_ context.Context, _ *memory.MemoryWriteJob, req memory.WriteRequest) (*memory.Memory, error) {
	if w.block != nil {
		select {
		case w.entered <- struct{}{}:
		default:
		}
		<-w.block
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.failFirst && !w.failedOnce {
		w.failedOnce = true
		return nil, errors.New("sql transaction failed")
	}
	w.successfulRows++
	item := &memory.Memory{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: int64(w.successfulRows), OwnerID: req.OwnerID}},
		MemoryType:      strings.TrimSpace(req.MemoryType),
		Content:         strings.TrimSpace(req.Content),
		Source:          memory.CanonicalSource(req.Source),
	}
	w.outboxEntries = append(w.outboxEntries, fakeOutboxEntry{
		OwnerID:      item.OwnerID,
		ResourceType: contextresource.TypeLongTermMemory,
		ResourceID:   strconv.FormatInt(item.ID, 10),
		Content:      item.Content,
		ContentHash:  contextresource.HashContent(item.Content),
	})
	return item, nil
}

func (w *fakeWriteJobWriter) writeCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func (w *fakeWriteJobWriter) outbox() []fakeOutboxEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fakeOutboxEntry(nil), w.outboxEntries...)
}

type fakeWriteWarnings struct {
	mu       sync.Mutex
	messages []string
}

func (w *fakeWriteWarnings) EmitWarning(_ context.Context, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, message)
}

func (w *fakeWriteWarnings) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.messages)
}

// TestMemoryWritePipelineEnqueueAdHocWithoutBlocking verifies finalization
// (enqueue) returns before a blocked downstream worker completes.
func TestMemoryWritePipelineEnqueueAdHocWithoutBlocking(t *testing.T) {
	repo := newFakeWriteJobRepo()
	pipeline := NewMemoryWritePipeline("test-worker", repo, nil, nil)
	seed := WriteJobRequest{
		OwnerID:        7,
		Source:         "ad_hoc",
		IdempotencyKey: "ad_hoc:42",
		Payload: memory.WriteRequest{
			OwnerID: 7, ConversationID: 3, RunID: 42,
			MemoryType: memory.TypeArchival, Content: "remember this preference",
		},
	}
	if err := pipeline.Enqueue(context.Background(), seed); err != nil {
		t.Fatalf("seed enqueue failed: %v", err)
	}
	writer := &fakeWriteJobWriter{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	blocking := NewMemoryWritePipeline("test-worker", repo, writer, nil)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_, _ = blocking.ProcessNext(context.Background())
	}()
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("downstream worker never claimed the job")
	}
	start := time.Now()
	if err := pipeline.Enqueue(context.Background(), seed); err != nil {
		t.Fatalf("finalization enqueue failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("finalization enqueue took %s; must not wait for the downstream worker", elapsed)
	}
	select {
	case <-workerDone:
		t.Fatal("finalization waited for downstream worker completion")
	case <-time.After(50 * time.Millisecond):
	}
	close(writer.block)
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never finished after release")
	}
}

// TestMemoryWritePipelineNoOpWhenPhaseOneFindsNoSignal verifies an extraction
// payload with no signal inserts no memory row and completes the job as no-op.
func TestMemoryWritePipelineNoOpWhenPhaseOneFindsNoSignal(t *testing.T) {
	repo := newFakeWriteJobRepo()
	writer := &fakeWriteJobWriter{}
	pipeline := NewMemoryWritePipeline("test-worker", repo, writer, nil)
	if err := pipeline.Enqueue(context.Background(), WriteJobRequest{
		OwnerID: 7, Source: "extraction", IdempotencyKey: "extraction:1",
		Payload: memory.WriteRequest{OwnerID: 7},
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	processed, err := pipeline.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("no-signal job errored: %v", err)
	}
	if !processed {
		t.Fatal("expected the job to be processed")
	}
	if writer.writeCalls() != 0 {
		t.Fatalf("no-signal extraction inserted %d memory row(s), want zero", writer.writeCalls())
	}
	job, ok := repo.jobByKey(7, "extraction:1")
	if !ok {
		t.Fatal("job row disappeared")
	}
	if job.Status != memory.WriteJobStatusCompleted {
		t.Fatalf("no-op job status=%s, want %s", job.Status, memory.WriteJobStatusCompleted)
	}
}

// TestMemoryWritePipelineKeepRunSuccessfulWhenEnqueueFails verifies queue
// failure is fail-open: the run keeps succeeding, the error is observable and
// a warning event is emitted.
func TestMemoryWritePipelineKeepRunSuccessfulWhenEnqueueFails(t *testing.T) {
	repo := newFakeWriteJobRepo()
	repo.failCreate = errors.New("queue unavailable")
	warnings := &fakeWriteWarnings{}
	pipeline := NewMemoryWritePipeline("test-worker", repo, &fakeWriteJobWriter{}, warnings)
	err := pipeline.Enqueue(context.Background(), WriteJobRequest{
		OwnerID: 7, Source: "ad_hoc", IdempotencyKey: "ad_hoc:9",
		Payload: memory.WriteRequest{OwnerID: 7, MemoryType: memory.TypeArchival, Content: "note"},
	})
	if err == nil {
		t.Fatal("expected enqueue error when the queue is unavailable")
	}
	if warnings.count() != 1 {
		t.Fatalf("expected one warning event, got %d", warnings.count())
	}
	// Finalization must treat the enqueue error as non-fatal: the run stays
	// successful and the note write is simply lost for retry/DLQ handling.
	adapter := NewAdHocWriteJobAdapter(pipeline)
	if _, noteErr := adapter.AppendAdHocNote(context.Background(), 7, 3, 9, "请记住这个偏好", "已记录"); noteErr == nil {
		t.Fatal("adapter swallowed the observable enqueue error")
	}
	if repo.countJobs() != 0 {
		t.Fatal("failed enqueue must not create a job row")
	}
}

// TestMemoryWritePipelineRetrySqlFailure verifies a SQL failure under lease is
// retried with backoff and a successful write produces exactly one memory row.
func TestMemoryWritePipelineRetrySqlFailure(t *testing.T) {
	repo := newFakeWriteJobRepo()
	writer := &fakeWriteJobWriter{failFirst: true}
	pipeline := NewMemoryWritePipeline("test-worker", repo, writer, nil)
	if err := pipeline.Enqueue(context.Background(), WriteJobRequest{
		OwnerID: 7, Source: "extraction", IdempotencyKey: "extraction:5",
		Payload: memory.WriteRequest{OwnerID: 7, MemoryType: memory.TypeEpisodic, ConversationID: 3, Content: "the user prefers concise answers"},
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	processed, err := pipeline.ProcessNext(context.Background())
	if !processed || err == nil {
		t.Fatalf("first attempt: processed=%v err=%v, want processed with sql error", processed, err)
	}
	job, _ := repo.jobByKey(7, "extraction:5")
	if job.Status != memory.WriteJobStatusPending {
		t.Fatalf("after sql failure job status=%s, want requeued %s", job.Status, memory.WriteJobStatusPending)
	}
	if job.DueAt == nil || !job.DueAt.After(time.Now().UTC()) {
		t.Fatalf("sql failure was not rescheduled with backoff: due_at=%v", job.DueAt)
	}
	if minDelay := writeJobRetryDelay(1); job.DueAt.Sub(time.Now().UTC()) < minDelay-time.Second {
		t.Fatalf("retry backoff too small: %s, want at least %s", job.DueAt.Sub(time.Now().UTC()), minDelay)
	}
	if job.LockedBy != "" || job.LockedAt != nil || job.LeaseExpiresAt != nil {
		t.Fatal("failed claim must release the lease")
	}
	repo.advance(2 * time.Minute)
	processed, err = pipeline.ProcessNext(context.Background())
	if !processed || err != nil {
		t.Fatalf("retry attempt: processed=%v err=%v, want success", processed, err)
	}
	job, _ = repo.jobByKey(7, "extraction:5")
	if job.Status != memory.WriteJobStatusCompleted {
		t.Fatalf("after retry job status=%s, want %s", job.Status, memory.WriteJobStatusCompleted)
	}
	if writer.successfulRows != 1 {
		t.Fatalf("successful memory rows=%d, want exactly one", writer.successfulRows)
	}
}

// TestMemoryWritePipelineEnqueueContextOutboxAfterCommit verifies the context
// outbox event fires exactly once with exact owner/resource/content version,
// and never on rollback.
func TestMemoryWritePipelineEnqueueContextOutboxAfterCommit(t *testing.T) {
	repo := newFakeWriteJobRepo()
	writer := &fakeWriteJobWriter{failFirst: true}
	pipeline := NewMemoryWritePipeline("test-worker", repo, writer, nil)
	if err := pipeline.Enqueue(context.Background(), WriteJobRequest{
		OwnerID: 11, Source: "extraction", IdempotencyKey: "extraction:6",
		Payload: memory.WriteRequest{OwnerID: 11, MemoryType: memory.TypeProfile, Content: "user prefers dark mode"},
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	_, err := pipeline.ProcessNext(context.Background())
	if err == nil {
		t.Fatal("expected rollback error on first attempt")
	}
	if entries := writer.outbox(); len(entries) != 0 {
		t.Fatalf("outbox emitted %d event(s) on rollback, want zero", len(entries))
	}
	repo.advance(2 * time.Minute)
	if _, err := pipeline.ProcessNext(context.Background()); err != nil {
		t.Fatalf("commit attempt failed: %v", err)
	}
	entries := writer.outbox()
	if len(entries) != 1 {
		t.Fatalf("outbox emitted %d event(s), want exactly one", len(entries))
	}
	entry := entries[0]
	if entry.OwnerID != 11 {
		t.Fatalf("outbox owner=%d, want 11", entry.OwnerID)
	}
	if entry.ResourceType != contextresource.TypeLongTermMemory {
		t.Fatalf("outbox resource type=%q, want %q", entry.ResourceType, contextresource.TypeLongTermMemory)
	}
	if entry.Content != "user prefers dark mode" {
		t.Fatalf("outbox content=%q, want exact committed version", entry.Content)
	}
	if entry.ContentHash != contextresource.HashContent("user prefers dark mode") {
		t.Fatalf("outbox content hash=%q, want hash of committed version", entry.ContentHash)
	}
	if entry.ResourceID != "1" {
		t.Fatalf("outbox resource id=%q, want the committed memory row id", entry.ResourceID)
	}
}

// TestMemoryWritePipelineRouteManualWriteThroughUnifiedJob verifies a manual
// write submits source `manual` through one idempotent job row, a leased SQL
// write and one post-commit context outbox event.
func TestMemoryWritePipelineRouteManualWriteThroughUnifiedJob(t *testing.T) {
	repo := newFakeWriteJobRepo()
	writer := &fakeWriteJobWriter{}
	pipeline := NewMemoryWritePipeline("test-worker", repo, writer, nil)
	req := WriteJobRequest{
		OwnerID: 7, Source: "manual", IdempotencyKey: "manual:1",
		Payload: memory.WriteRequest{OwnerID: 7, MemoryType: memory.TypeProfile, Title: "style", Content: "user prefers concise Chinese replies"},
	}
	if err := pipeline.Enqueue(context.Background(), req); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if err := pipeline.Enqueue(context.Background(), req); err != nil {
		t.Fatalf("duplicate enqueue failed: %v", err)
	}
	if repo.countJobs() != 1 {
		t.Fatalf("created %d job rows, want one idempotent row", repo.countJobs())
	}
	job, ok := repo.jobByKey(7, "manual:1")
	if !ok {
		t.Fatal("manual write job row missing")
	}
	if job.Source != "manual" {
		t.Fatalf("job source=%q, want %q", job.Source, "manual")
	}
	if job.Status != memory.WriteJobStatusPending {
		t.Fatalf("job status=%q, want %q", job.Status, memory.WriteJobStatusPending)
	}
	processed, err := pipeline.ProcessNext(context.Background())
	if !processed || err != nil {
		t.Fatalf("manual write processing: processed=%v err=%v", processed, err)
	}
	if writer.writeCalls() != 1 {
		t.Fatalf("leased sql write called %d time(s), want one", writer.writeCalls())
	}
	entries := writer.outbox()
	if len(entries) != 1 {
		t.Fatalf("outbox emitted %d event(s), want one", len(entries))
	}
	if entries[0].OwnerID != 7 || entries[0].Content != "user prefers concise Chinese replies" {
		t.Fatalf("outbox event does not match committed manual memory: %+v", entries[0])
	}
	job, _ = repo.jobByKey(7, "manual:1")
	if job.Status != memory.WriteJobStatusCompleted {
		t.Fatalf("job status after write=%s, want %s", job.Status, memory.WriteJobStatusCompleted)
	}
}
