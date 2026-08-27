package memory_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

// These tests pin the extraction write wiring (design Decisions 9 and 10,
// spec "Extraction writes flow through unified write jobs"): gated candidates
// become memory_write_jobs rows with source extraction and keys
// extraction:<job-id>:<index>; a job whose evidence yields nothing completes
// as no_output without any write; missing models fail into the linear extract
// backoff instead of dumping text.

// fakeMemoryRowRepo mirrors the production memories table contract used by
// SQLMemoryWriter: Create is conflict-tolerant on the unique
// (owner_id, deduplication_key) index — ON CONFLICT DoNothing followed by a
// re-read of the winning row — so cross-job deduplication converges on one
// row.
type fakeMemoryRowRepo struct {
	mu           sync.Mutex
	rows         map[int64]*memory.Memory
	byDedup      map[string]int64
	nextID       int64
	creates      int
	conflictHits int
}

func newFakeMemoryRowRepo() *fakeMemoryRowRepo {
	return &fakeMemoryRowRepo{rows: map[int64]*memory.Memory{}, byDedup: map[string]int64{}, nextID: 1}
}

func (r *fakeMemoryRowRepo) dedupKey(ownerID int64, key string) string {
	return strconv.FormatInt(ownerID, 10) + ":" + key
}

func (r *fakeMemoryRowRepo) Create(_ context.Context, item *memory.Memory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creates++
	if item.DeduplicationKey != nil {
		if id, ok := r.byDedup[r.dedupKey(item.OwnerID, *item.DeduplicationKey)]; ok {
			r.conflictHits++
			*item = *r.rows[id]
			return nil
		}
	}
	item.ID = r.nextID
	r.nextID++
	clone := *item
	r.rows[item.ID] = &clone
	if item.DeduplicationKey != nil {
		r.byDedup[r.dedupKey(item.OwnerID, *item.DeduplicationKey)] = item.ID
	}
	return nil
}

func (r *fakeMemoryRowRepo) rowCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

func (r *fakeMemoryRowRepo) createCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creates
}

func (r *fakeMemoryRowRepo) conflictCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conflictHits
}

func (r *fakeMemoryRowRepo) firstRow() *memory.Memory {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		clone := *row
		return &clone
	}
	return nil
}

// hasDedupKey reports whether a row of the given source exists whose
// DeduplicationKey equals key verbatim.
func (r *fakeMemoryRowRepo) hasDedupKey(ownerID int64, key, source string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byDedup[r.dedupKey(ownerID, key)]
	return ok && r.rows[id].Source == source
}

func (r *fakeMemoryRowRepo) Update(context.Context, *memory.Memory) error { return nil }
func (r *fakeMemoryRowRepo) FindByID(context.Context, int64, int64) (*memory.Memory, error) {
	return nil, errNotFound
}
func (r *fakeMemoryRowRepo) FindByIDs(context.Context, int64, []int64) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRowRepo) List(context.Context, int64, []string, *int64, int, int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRowRepo) ListForRead(context.Context, int64, []string, *int64, int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRowRepo) ListBySources(context.Context, int64, []string, int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRowRepo) ListByLevel(context.Context, int64, string, []string, int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeMemoryRowRepo) ListActiveOwnerIDs(context.Context, int) ([]int64, error) {
	return nil, nil
}
func (r *fakeMemoryRowRepo) IncrementUsageCount(context.Context, int64, int64) error { return nil }
func (r *fakeMemoryRowRepo) IncrementPromotionCount(context.Context, int64, int64) error {
	return nil
}
func (r *fakeMemoryRowRepo) SoftDelete(context.Context, int64, int64) error { return nil }
func (r *fakeMemoryRowRepo) MarkUsed(context.Context, int64, []int64) error { return nil }
func (r *fakeMemoryRowRepo) MarkExpired(context.Context, int64, int) (int64, error) {
	return 0, nil
}
func (r *fakeMemoryRowRepo) UpdateDecayedImportance(context.Context, int64, float64) (int64, error) {
	return 0, nil
}

var _ memory.Repository = (*fakeMemoryRowRepo)(nil)

// newWriteWiringPipeline builds the unified write pipeline over the in-memory
// job repository; the writer defaults to the real SQLMemoryWriter backed by
// the in-memory memories table so deduplication is exercised end to end.
func newWriteWiringPipeline(rows *fakeMemoryRowRepo) (*MemoryWritePipeline, *fakeWriteJobRepo) {
	jobs := newFakeWriteJobRepo()
	return NewMemoryWritePipeline("test-worker", jobs, NewSQLMemoryWriter(rows), nil), jobs
}

// newWriteWiringWorker wires the durable worker with the consolidation
// collaborators (empty evidence sources: consolidation stays a no-op) and the
// extraction write pipeline under test.
func newWriteWiringWorker(chat llm.ChatClient, jobs memory.ExtractionJobRepository, messages DreamMessageRepository, pipeline *MemoryWritePipeline, artifacts *fakeArtifactRepo) *DurableMemoryWorker {
	projection := NewConsolidationProjection(artifacts)
	projection.Now = projectionTestNow
	return NewDurableMemoryWorker(chat, messages, jobs, nil,
		DurableMemoryConfig{Enabled: true, Model: "test-extraction-model"},
		"test-worker",
		WithConsolidationProjection(projection),
		WithConsolidationSources(newFakeMemorySourceRepo()),
		WithExtractionWrites(pipeline),
	)
}

func writeWiringCandidatesJSON(t *testing.T, candidates []ExtractionCandidate) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"candidates": candidates})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func listWriteJobs(t *testing.T, repo *fakeWriteJobRepo) []*memory.MemoryWriteJob {
	t.Helper()
	jobs := make([]*memory.MemoryWriteJob, 0, len(repo.jobs))
	for id := int64(1); id <= repo.nextID; id++ {
		if job, ok := repo.jobs[id]; ok {
			clone := *job
			jobs = append(jobs, &clone)
		}
	}
	return jobs
}

func TestExtractionWriteWiring(t *testing.T) {
	t.Run("shouldEnqueueGatedCandidatesWithExtractionKeys", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{42: seedExtractionJob(42, 1)}}
		jobsRepo.nextID = 42
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "we pinned the base image after the glibc incident")}}
		candidates := []ExtractionCandidate{
			{Title: "Base image pin", Content: "Pin the base image before deploying.", Type: "lesson", Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1"}},
			{Title: "Postmortem preference", Content: "The user asks for a postmortem after incidents.", Type: "preference", Confidence: 0.75, Importance: 0.55, EvidenceRefs: []string{"messages:1"}},
		}
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) {
			return writeWiringCandidatesJSON(t, candidates), nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newWriteWiringWorker(chat, jobsRepo, messages, pipeline, newFakeArtifactRepo())

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 42, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		job := jobsRepo.jobs[42]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != 2 {
			t.Fatalf("write jobs = %d, want one per accepted candidate", len(enqueued))
		}
		wantKeys := []string{"extraction:42:0", "extraction:42:1"}
		for index, want := range wantKeys {
			got := enqueued[index]
			if got.IdempotencyKey != want {
				t.Fatalf("write job %d key = %q, want %q", index, got.IdempotencyKey, want)
			}
			if got.Source != "extraction" {
				t.Fatalf("write job %d source = %q, want extraction", index, got.Source)
			}
			if got.Status != memory.WriteJobStatusPending {
				t.Fatalf("write job %d status = %q, want pending", index, got.Status)
			}
			var payload memory.WriteRequest
			if err := json.Unmarshal(got.PayloadJSON, &payload); err != nil {
				t.Fatalf("write job %d payload: %v", index, err)
			}
			if payload.Content != candidates[index].Content || payload.Title != candidates[index].Title {
				t.Fatalf("write job %d payload = %+v, want candidate content and title", index, payload)
			}
			if payload.Source != "extraction" {
				t.Fatalf("write job %d payload source = %q, want extraction", index, payload.Source)
			}
		}
	})

	t.Run("shouldCompleteNoOutputWithoutWrites", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "hello there, nice weather today")}}
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) { return `{"candidates":[]}`, nil }}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newWriteWiringWorker(chat, jobsRepo, messages, pipeline, newFakeArtifactRepo())

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		job := jobsRepo.jobs[1]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
		result := decodeDurableResult(t, job.ResultJSON)
		if result.Outcome != durableExtractionOutcomeNoOutput {
			t.Fatalf("outcome = %q, want %q", result.Outcome, durableExtractionOutcomeNoOutput)
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatalf("no_output job enqueued write jobs, want zero")
		}
		for drained := true; drained; {
			var err error
			drained, err = pipeline.ProcessNext(context.Background())
			if err != nil {
				t.Fatalf("drain write pipeline: %v", err)
			}
		}
		if rows.rowCount() != 0 {
			t.Fatalf("no_output job produced %d memory row(s), want zero", rows.rowCount())
		}
	})

	t.Run("shouldDeduplicateIdenticalContentAcrossJobs", func(t *testing.T) {
		const normalizedContent = "Pin the base image before deploying."
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{
			42: seedExtractionJob(42, 1),
			43: seedExtractionJob(43, 2),
		}}
		jobsRepo.nextID = 43
		messages := &fakeDreamMessages{items: []conversation.Message{
			extractionMessageRow(1, "first window evidence"),
			extractionMessageRow(2, "second window evidence"),
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		artifacts := newFakeArtifactRepo()

		runJob := func(jobID int64, rawContent string) {
			chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) {
				return writeWiringCandidatesJSON(t, []ExtractionCandidate{{
					Title: "Base image pin", Content: rawContent, Type: "lesson",
					Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1"},
				}}), nil
			}}
			worker := newWriteWiringWorker(chat, jobsRepo, messages, pipeline, artifacts)
			if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: jobID, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
				t.Fatalf("handle job %d: %v", jobID, err)
			}
		}
		// Two different jobs emit the same type and content modulo whitespace:
		// normalize(trim + collapse) must collapse them onto one dedup key.
		runJob(42, "Pin\tthe   base image\nbefore deploying.")
		runJob(43, normalizedContent)

		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != 2 {
			t.Fatalf("write jobs = %d, want one per extraction job", len(enqueued))
		}
		if enqueued[0].IdempotencyKey != "extraction:42:0" || enqueued[1].IdempotencyKey != "extraction:43:0" {
			t.Fatalf("write job keys = %q,%q, want extraction:42:0 and extraction:43:0", enqueued[0].IdempotencyKey, enqueued[1].IdempotencyKey)
		}

		for drained, drainedErr := true, error(nil); drained; {
			drained, drainedErr = pipeline.ProcessNext(context.Background())
			if drainedErr != nil {
				t.Fatalf("drain write pipeline: %v", drainedErr)
			}
		}

		if rows.rowCount() != 1 {
			t.Fatalf("memory rows = %d, want exactly one after cross-job dedup", rows.rowCount())
		}
		if rows.conflictCount() != 1 {
			t.Fatalf("ON CONFLICT hits = %d, want the second write to hit the existing row", rows.conflictCount())
		}
		row := rows.firstRow()
		if row.DeduplicationKey == nil {
			t.Fatal("deduplicated row lost its deduplication key")
		}
		dedupKey := *row.DeduplicationKey
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(dedupKey) {
			t.Fatalf("deduplication key = %q, want 64-char lowercase sha256 hex", dedupKey)
		}
		sum := sha256.Sum256([]byte(memory.TypeArchival + "\n" + normalizedContent))
		if want := hex.EncodeToString(sum[:]); dedupKey != want {
			t.Fatalf("deduplication key = %q, want sha256(type+\\n+normalize(content)) = %q", dedupKey, want)
		}
		if row.Source != "extraction" {
			t.Fatalf("deduplicated row source = %q, want extraction", row.Source)
		}
	})

	t.Run("shouldFailWithoutModelInsteadOfDumpingText", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "evidence that must never be dumped")}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		// chatClient=nil: the retired raw-text dump must not run; the job
		// fails into the LINEAR extract backoff instead.
		worker := newWriteWiringWorker(nil, jobsRepo, messages, pipeline, newFakeArtifactRepo())
		before := time.Now().UTC()

		err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation})
		if err == nil || !strings.Contains(err.Error(), "model") {
			t.Fatalf("handle error = %v, want the missing-model failure", err)
		}
		job := jobsRepo.jobs[1]
		if job.Status != string(memory.ExtractionPending) {
			t.Fatalf("job status = %q, want back to pending for retry", job.Status)
		}
		if job.AttemptCount != 1 || job.DueAt == nil {
			t.Fatalf("attempt=%d due_at=%v, want attempt 1 with a backoff", job.AttemptCount, job.DueAt)
		}
		want := before.Add(time.Duration(job.AttemptCount+1) * time.Minute)
		if delta := job.DueAt.Sub(want); delta < -30*time.Second || delta > 90*time.Second {
			t.Fatalf("due_at = %s, want linear backoff %s (attempt+1 minutes)", job.DueAt.Format(time.RFC3339), want.Format(time.RFC3339))
		}
		if job.Phase2AttemptCount != 0 || strings.HasPrefix(job.ErrorMessage, "phase2:") {
			t.Fatalf("extract no-model failure leaked into the phase2 channel: phase2=%d error=%q", job.Phase2AttemptCount, job.ErrorMessage)
		}
		// Nothing is dumped: no persisted result, no write jobs, no rows.
		if len(job.ResultJSON) != 0 {
			t.Fatalf("failed extraction persisted result_json: %s", job.ResultJSON)
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("failed extraction enqueued write jobs, want zero")
		}
		if rows.rowCount() != 0 {
			t.Fatalf("failed extraction produced %d memory row(s), want zero", rows.rowCount())
		}
	})

	t.Run("shouldReenqueueCandidatesAfterTransientEnqueueFailure", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{55: seedExtractionJob(55, 1)}}
		jobsRepo.nextID = 55
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "we pinned the base image after the glibc incident")}}
		candidates := []ExtractionCandidate{
			{Title: "Base image pin", Content: "Pin the base image before deploying.", Type: "lesson", Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1"}},
			{Title: "Postmortem preference", Content: "The user asks for a postmortem after incidents.", Type: "preference", Confidence: 0.75, Importance: 0.55, EvidenceRefs: []string{"messages:1"}},
		}
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) {
			return writeWiringCandidatesJSON(t, candidates), nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newWriteWiringWorker(chat, jobsRepo, messages, pipeline, newFakeArtifactRepo())
		payload := DurableMemoryPayload{JobID: 55, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}

		// Pass 1: the write-job store transiently rejects the FIRST enqueue.
		writeJobs.failCreate = errors.New("write job store unavailable")
		before := time.Now().UTC()
		if err := worker.Handle(context.Background(), payload); err == nil {
			t.Fatal("transient enqueue failure must fail the extraction attempt")
		}
		writeJobs.failCreate = nil

		job := jobsRepo.jobs[55]
		if job.Status != string(memory.ExtractionPending) {
			t.Fatalf("job status = %q, want back to pending for retry", job.Status)
		}
		if job.AttemptCount != 1 || job.DueAt == nil {
			t.Fatalf("attempt=%d due_at=%v, want attempt 1 with a backoff", job.AttemptCount, job.DueAt)
		}
		want := before.Add(time.Duration(job.AttemptCount+1) * time.Minute)
		if delta := job.DueAt.Sub(want); delta < -30*time.Second || delta > 90*time.Second {
			t.Fatalf("due_at = %s, want linear backoff %s (attempt+1 minutes)", job.DueAt.Format(time.RFC3339), want.Format(time.RFC3339))
		}
		if !strings.Contains(job.ErrorMessage, "write job store unavailable") {
			t.Fatalf("error message = %q, want the surfaced enqueue failure", job.ErrorMessage)
		}
		// The persisted result keeps the extracted chunks with an EMPTY
		// outcome: a terminal outcome persisted before a successful enqueue
		// would make the retry skip gate+enqueue and lose the candidates.
		partial := decodeDurableResult(t, job.ResultJSON)
		if len(partial.Chunks[0]) != 2 {
			t.Fatalf("failed attempt lost the extracted chunk: %s", job.ResultJSON)
		}
		if partial.Outcome != "" {
			t.Fatalf("failed enqueue persisted terminal outcome %q, want empty so the retry re-enters gate+enqueue", partial.Outcome)
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("failed first Create still enqueued write jobs, want zero")
		}

		// Retry with a healthy store: the completed chunk must be skipped
		// (zero new extraction-model calls), the gate is pure and idempotent,
		// and the re-enqueue lands every candidate exactly once.
		job.DueAt = nil
		if err := worker.Handle(context.Background(), payload); err != nil {
			t.Fatalf("retry handle: %v", err)
		}
		if chat.calls() != 1 {
			t.Fatalf("total extraction model calls = %d, want 1: the retry must skip the completed chunk", chat.calls())
		}
		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != len(candidates) {
			t.Fatalf("write jobs after retry = %d, want one per candidate with no duplicates", len(enqueued))
		}
		wantKeys := []string{"extraction:55:0", "extraction:55:1"}
		for index, wantKey := range wantKeys {
			if enqueued[index].IdempotencyKey != wantKey {
				t.Fatalf("write job %d key = %q, want %q", index, enqueued[index].IdempotencyKey, wantKey)
			}
		}
		final := jobsRepo.jobs[55]
		if final.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("final status = %q, want completed", final.Status)
		}
		result := decodeDurableResult(t, final.ResultJSON)
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("final outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
		if len(result.Chunks[0]) != 2 {
			t.Fatalf("final result lost the extracted chunk: %s", final.ResultJSON)
		}
	})

	t.Run("shouldKeepNoOutputOutcomeForShadowWindow", func(t *testing.T) {
		// A successor completes with a boundary BEYOND this job's through
		// before the job runs: previousBoundary shadows the window into
		// nothing. The job completes no_output without any model call and
		// without touching the write pipeline (accepted stays nil, so the
		// enqueue step is a successful no-op).
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{
			1: seedExtractionJob(1, 5),
			2: {
				BaseModel:        domain.BaseModel{ID: 2, OwnerID: scheduleTestOwner},
				ConversationID:   scheduleTestConversation,
				TriggerReason:    "durable",
				ThroughMessageID: 7,
				Status:           string(memory.ExtractionCompleted),
			},
		}}
		jobsRepo.nextID = 2
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) { return `{"candidates":[]}`, nil }}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newWriteWiringWorker(chat, jobsRepo, &fakeDreamMessages{}, pipeline, newFakeArtifactRepo())

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		job := jobsRepo.jobs[1]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
		result := decodeDurableResult(t, job.ResultJSON)
		if result.Outcome != durableExtractionOutcomeNoOutput {
			t.Fatalf("outcome = %q, want %q for the shadowed window", result.Outcome, durableExtractionOutcomeNoOutput)
		}
		if len(result.Chunks) != 0 {
			t.Fatalf("shadow window stored %d chunk(s), want none", len(result.Chunks))
		}
		if chat.calls() != 0 {
			t.Fatalf("shadow window called the extraction model %d time(s), want zero", chat.calls())
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("shadow window enqueued write jobs, want zero")
		}
		if rows.rowCount() != 0 {
			t.Fatalf("shadow window produced %d memory row(s), want zero", rows.rowCount())
		}
	})
}
