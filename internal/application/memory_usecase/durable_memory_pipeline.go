package memory_usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	"agentcanvas/internal/pkg/config"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type DreamMessageRepository interface {
	ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversation.Message, error)
	ListActiveThrough(ctx context.Context, ownerID, conversationID, throughMessageID int64) ([]conversation.Message, error)
}

// Optional range readers keep the old message repository contract working while
// allowing Dream to avoid loading the whole conversation for every extraction.
type dreamMessageBoundaryReader interface {
	LatestActiveMessageID(ctx context.Context, ownerID, conversationID int64) (int64, error)
}

type dreamMessageRangeReader interface {
	ListActiveAfterThrough(ctx context.Context, ownerID, conversationID, afterMessageID, throughMessageID int64) ([]conversation.Message, error)
}

// dreamMessageArchiveRangeReader reads an extraction window INCLUDING
// soft-archived rows (design Decision 2). Repositories implementing it give
// the durable worker the compaction-safe archive-inclusive window; anything
// else keeps the historical active-only reads unchanged.
type dreamMessageArchiveRangeReader interface {
	ListThroughIncludingArchived(ctx context.Context, ownerID, conversationID, afterID, throughID int64) ([]conversation.Message, error)
}

// DurableMemoryJobType is the only production memory-generation job. Candidate
// proposals and retention-tier schedulers are deliberately not part of this
// pipeline: a rollout is extracted once, then one consolidation writer owns
// the durable artifacts.
const DurableMemoryJobType = "memory:durable"

const (
	durableStage1Lease      = 2 * time.Minute
	durablePhase2Lease      = 10 * time.Minute
	durableMaxRolloutLen    = 120_000
	durablePhase2MaxBackoff = 6 * time.Hour
)

// ponytail: one process-local lock is the smallest safe fallback when Redis
// is unavailable; deployments that need cross-process coordination must keep
// Redis enabled (the normal production path).
var durablePhase2FallbackLock sync.Mutex
var durableConversationFallbackLocks sync.Map

type DurableMemoryConfig struct {
	Enabled     bool
	IdleTimeout time.Duration
	Provider    llm.ChatProviderConfig
	Model       string
	Root        string
}

func NewDurableMemoryConfig(cfg config.MemoryDreamConfig) DurableMemoryConfig {
	root := strings.TrimSpace(os.Getenv("AGENTCANVAS_MEMORY_ROOT"))
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, ".agentcanvas", "memories")
		} else {
			root = filepath.Join(os.TempDir(), "agentcanvas", "memories")
		}
	}
	idle := time.Duration(cfg.IdleTimeoutSeconds) * time.Second
	if idle <= 0 {
		idle = 6 * time.Hour
	}
	return DurableMemoryConfig{
		Enabled:     cfg.Enabled,
		IdleTimeout: idle,
		Provider:    llm.ChatProviderConfig{ProviderType: cfg.LLMProviderType, BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey},
		Model:       strings.TrimSpace(cfg.LLMModel),
		Root:        root,
	}
}

type DurableMemoryPayload struct {
	JobID          int64 `json:"job_id"`
	OwnerID        int64 `json:"owner_id"`
	ConversationID int64 `json:"conversation_id"`
}

type durablePhase2RetryReader interface {
	ListPhase2Retries(ctx context.Context, limit int) ([]memory.ExtractionJob, error)
}

// DurableMemoryWorkerOptions carries the Phase-2 consolidation collaborators
// that replaced the retired file workspace. The projection is required for
// production; tests may omit the collaborators to exercise Phase-1 paths in
// isolation.
type DurableMemoryWorkerOptions struct {
	Projection *ConsolidationProjection
	Sources    ConsolidationSourceReader
	Writes     ExtractionWriteEnqueuer
}

// ConsolidationSourceReader lists durable evidence memory rows so Phase-2
// consolidation consumes the canonical memory rows instead of job rows. The
// returned rows are identified by memories.id: artifact source refs MUST carry
// the memory ID, never a write-job or durable-job ID.
type ConsolidationSourceReader interface {
	ListBySources(ctx context.Context, ownerID int64, sources []string, limit int) ([]memory.Memory, error)
}

// ExtractionWriteEnqueuer enqueues gated extraction candidates as unified
// memory write jobs (design Decision 9). *MemoryWritePipeline satisfies it;
// the durable worker never writes memory rows itself.
type ExtractionWriteEnqueuer interface {
	Enqueue(ctx context.Context, req WriteJobRequest) error
}

func WithConsolidationProjection(projection *ConsolidationProjection) func(*DurableMemoryWorkerOptions) {
	return func(options *DurableMemoryWorkerOptions) { options.Projection = projection }
}

func WithConsolidationSources(reader ConsolidationSourceReader) func(*DurableMemoryWorkerOptions) {
	return func(options *DurableMemoryWorkerOptions) { options.Sources = reader }
}

func WithExtractionWrites(enqueuer ExtractionWriteEnqueuer) func(*DurableMemoryWorkerOptions) {
	return func(options *DurableMemoryWorkerOptions) { options.Writes = enqueuer }
}

type DurableMemoryWorker struct {
	chatClient llm.ChatClient
	messages   DreamMessageRepository
	jobs       memory.ExtractionJobRepository
	redis      *redis.Client
	cfg        DurableMemoryConfig
	workerID   string

	projection *ConsolidationProjection
	sources    ConsolidationSourceReader
	writes     ExtractionWriteEnqueuer
}

func NewDurableMemoryWorker(chatClient llm.ChatClient, messages DreamMessageRepository, jobs memory.ExtractionJobRepository, redisClient *redis.Client, cfg DurableMemoryConfig, workerID string, optionFns ...func(*DurableMemoryWorkerOptions)) *DurableMemoryWorker {
	options := &DurableMemoryWorkerOptions{}
	for _, apply := range optionFns {
		if apply != nil {
			apply(options)
		}
	}
	return &DurableMemoryWorker{chatClient: chatClient, messages: messages, jobs: jobs, redis: redisClient, cfg: cfg, workerID: workerID, projection: options.Projection, sources: options.Sources, writes: options.Writes}
}

// durableBoundaryScheduler is the atomic, transactional debounce implemented by
// the SQL repository (SELECT ... FOR UPDATE on the conversation's latest
// durable row, then refresh/successor/create inside the same transaction).
type durableBoundaryScheduler interface {
	ScheduleDurableBoundary(ctx context.Context, ownerID, conversationID, throughMessageID int64, dueAt time.Time) (*memory.ExtractionJob, bool, error)
}

// NewDurableMemoryTrigger debounces durable extraction per conversation: the
// conversation keeps at most one pending job row, refreshed in place while it
// waits, succeeded exactly once while it runs. The queue wakeup is published
// only when a new row is created, never on a refresh.
func NewDurableMemoryTrigger(jobQueue queueinfra.JobQueue, redisClient *redis.Client, cfg DurableMemoryConfig, jobs memory.ExtractionJobRepository, messages DreamMessageRepository) func(context.Context, int64, int64, int) {
	if !cfg.Enabled || jobs == nil || messages == nil {
		return nil
	}
	return func(ctx context.Context, ownerID, conversationID int64, _ int) {
		if ownerID <= 0 || conversationID <= 0 {
			return
		}
		through, err := latestDurableMessageID(ctx, messages, ownerID, conversationID)
		if err != nil || through <= 0 {
			return
		}
		idle := cfg.IdleTimeout
		if idle <= 0 {
			idle = time.Minute
		}
		dueAt := time.Now().UTC().Add(idle)
		job, created, err := scheduleDurableBoundary(ctx, jobs, ownerID, conversationID, through, dueAt)
		if err != nil || job == nil || !created || jobQueue == nil {
			return
		}
		if job.DueAt == nil {
			return
		}
		queueJob := queueinfra.Job{SchemaVersion: queueinfra.JobSchemaVersion, ID: fmt.Sprintf("durable-job-%d", job.ID), Type: DurableMemoryJobType, Payload: map[string]any{"job_id": job.ID, "owner_id": ownerID, "conversation_id": conversationID}, AvailableAt: *job.DueAt}
		_ = jobQueue.Publish(ctx, queueJob)
	}
}

// scheduleDurableBoundary routes the debounce decision: repositories with the
// transactional scheduler take the FOR UPDATE path; anything else (in-memory
// fakes, future stores) composes the same semantics from the targeted
// primitives, including the defensive zero-row-refresh fallback.
func scheduleDurableBoundary(ctx context.Context, jobs memory.ExtractionJobRepository, ownerID, conversationID, throughMessageID int64, dueAt time.Time) (*memory.ExtractionJob, bool, error) {
	if scheduler, ok := jobs.(durableBoundaryScheduler); ok {
		return scheduler.ScheduleDurableBoundary(ctx, ownerID, conversationID, throughMessageID, dueAt)
	}
	return scheduleDurableBoundaryStepwise(ctx, jobs, ownerID, conversationID, throughMessageID, dueAt)
}

// scheduleDurableBoundaryStepwise is the primitive-composed debounce used when
// the repository does not provide the single-transaction scheduler. The
// conditional refresh reports the concurrent-claim race as refreshed=false and
// falls back to a successor; the unique (owner_id, idempotency_key) key is the
// final guard against duplicate successors, resolved by re-reading the winner.
func scheduleDurableBoundaryStepwise(ctx context.Context, jobs memory.ExtractionJobRepository, ownerID, conversationID, throughMessageID int64, dueAt time.Time) (*memory.ExtractionJob, bool, error) {
	latest, err := jobs.LatestDurableJob(ctx, ownerID, conversationID)
	if err != nil {
		return nil, false, err
	}
	if latest != nil && latest.Status == string(memory.ExtractionPending) {
		refreshed, refreshErr := jobs.RefreshPendingBoundary(ctx, ownerID, latest.ID, throughMessageID, dueAt)
		if refreshErr != nil {
			return nil, false, refreshErr
		}
		if refreshed {
			latest.ThroughMessageID = throughMessageID
			due := dueAt
			latest.DueAt = &due
			return latest, false, nil
		}
		// Zero rows affected: the pending row was claimed concurrently. Fall
		// through and create its successor.
	}
	row := &memory.ExtractionJob{
		BaseModel:        domain.BaseModel{OwnerID: ownerID},
		ConversationID:   conversationID,
		IdempotencyKey:   durableBoundaryKey(ownerID, conversationID, latest),
		TriggerReason:    "durable",
		ThroughMessageID: throughMessageID,
		Status:           string(memory.ExtractionPending),
		DueAt:            durablePtrTime(dueAt),
	}
	if err := jobs.Create(ctx, row); err != nil {
		existing, findErr := jobs.FindByIdempotencyKey(ctx, ownerID, row.IdempotencyKey)
		if findErr != nil || existing == nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	return row, true, nil
}

// durableBoundaryKey encodes the generation in the idempotency key: the
// conversation's first job is :initial; every later job is the successor of
// the latest job observed at schedule time.
func durableBoundaryKey(ownerID, conversationID int64, latest *memory.ExtractionJob) string {
	if latest == nil {
		return fmt.Sprintf("durable:%d:%d:initial", ownerID, conversationID)
	}
	return fmt.Sprintf("durable:%d:%d:after-job:%d", ownerID, conversationID, latest.ID)
}

func (w *DurableMemoryWorker) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.jobs == nil || !w.cfg.Enabled {
		return false, nil
	}
	jobs, err := w.jobs.ListPending(ctx, 50)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.TriggerReason != "durable" {
			continue
		}
		return true, w.Handle(ctx, DurableMemoryPayload{JobID: job.ID, OwnerID: job.OwnerID, ConversationID: job.ConversationID})
	}
	if retries, ok := w.jobs.(durablePhase2RetryReader); ok {
		pending, listErr := retries.ListPhase2Retries(ctx, 20)
		if listErr != nil {
			return false, listErr
		}
		for _, job := range pending {
			if job.DueAt != nil && job.DueAt.After(time.Now().UTC()) {
				continue
			}
			if err := w.consolidate(ctx, job.OwnerID); err != nil {
				_ = w.deferPhase2Retry(context.WithoutCancel(ctx), &job, err)
				return true, err
			}
			job.ErrorMessage = ""
			job.DueAt = nil
			if err := w.jobs.Update(ctx, &job); err != nil {
				return true, err
			}
			return true, nil
		}
	}
	return false, nil
}

func (w *DurableMemoryWorker) Handle(ctx context.Context, payload DurableMemoryPayload) (retErr error) {
	if w == nil || w.jobs == nil || w.messages == nil || !w.cfg.Enabled {
		return nil
	}
	job, claimed, err := w.claim(ctx, payload)
	if err != nil || !claimed {
		return err
	}
	// Keep the ownership token local for the whole Phase 1 attempt. The job
	// fields are intentionally cleared before a terminal update, so using
	// job.LockedBy as the dispatch signal would silently downgrade that update
	// to an unconditional Save and allow a lease race.
	leaseOwner := ""
	if _, ok := w.jobs.(memory.ExtractionLeaseRepository); ok && job.LockedBy == w.workerID {
		leaseOwner = w.workerID
	}
	leaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	if leased, ok := w.jobs.(memory.ExtractionLeaseRepository); ok && leaseOwner != "" {
		go w.heartbeatStage1Lease(leaseCtx, leased, job.ID)
		ctx = leaseCtx
	}
	defer func() {
		if retErr == nil || job.Status == string(memory.ExtractionCompleted) {
			return
		}
		job.Status = string(memory.ExtractionPending)
		job.ErrorMessage = retErr.Error()
		job.DueAt = durablePtrTime(time.Now().UTC().Add(time.Duration(job.AttemptCount+1) * time.Minute))
		job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
		_ = w.updateJob(context.WithoutCancel(ctx), job, leaseOwner)
	}()

	if job.DueAt != nil && job.DueAt.After(time.Now().UTC()) {
		job.Status = string(memory.ExtractionPending)
		job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
		return w.updateJob(ctx, job, leaseOwner)
	}
	if job.ThroughMessageID <= 0 {
		job.ThroughMessageID, err = latestDurableMessageID(ctx, w.messages, job.OwnerID, job.ConversationID)
		if err != nil || job.ThroughMessageID <= 0 {
			return err
		}
	}
	unlock, err := w.conversationLock(ctx, job.OwnerID, job.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	// A stored Phase 1 result is immutable. Consolidation retries must never
	// call the extraction model a second time. The one exception is a PARTIAL
	// chunked result (design Decision 8): completed chunks are skipped and
	// only the missing chunks are re-sent.
	var accepted []ExtractionCandidate
	result, resumable := decodeDurableExtractionResult(job.ResultJSON)
	if len(bytes.TrimSpace(job.ResultJSON)) == 0 || (resumable && result.Outcome == "") {
		previousThrough := w.previousBoundary(ctx, job)
		if previousThrough >= job.ThroughMessageID && job.ThroughMessageID > 0 {
			result = durableExtractionResult{Chunks: map[int][]ExtractionCandidate{}, Outcome: durableExtractionOutcomeNoOutput}
		} else {
			// Resume index soundness: chunk indices are positional within the
			// plan recomputed from THIS attempt's window. If the window moved
			// since the partial result was persisted (a successor completed
			// and previousBoundary advanced), index N no longer maps to the
			// same evidence — discard the partial chunks and re-extract from
			// scratch rather than keep stale candidates. A stored marker pair
			// of (0,0) is a pre-markers payload, which can never be validated
			// and is discarded the same way.
			if result.WindowAfter != previousThrough || result.WindowThrough != job.ThroughMessageID {
				result = durableExtractionResult{Chunks: map[int][]ExtractionCandidate{}}
			}
			messages, messageErr := w.messagesThrough(ctx, job.OwnerID, job.ConversationID, previousThrough, job.ThroughMessageID)
			if messageErr != nil {
				return messageErr
			}
			result.WindowAfter = previousThrough
			result.WindowThrough = job.ThroughMessageID
			units := NewEvidenceRenderer().Render(messages)
			chunks := NewEvidenceChunker().Chunk(units)
			if extractErr := w.extractChunks(ctx, job, &result, chunks, leaseOwner); extractErr != nil {
				return extractErr
			}
			if result.Outcome == "" {
				// Quality gates run on the merged-or-single-chunk candidate
				// stream (design Decision 7); a job whose evidence yields
				// nothing completes as no_output and writes nothing. The
				// terminal outcome stays OUT of result_json until the write
				// jobs below are durably enqueued.
				accepted = gateExtractionResult(&result)
			}
		}
		job.ResultJSON, err = json.Marshal(result)
		if err != nil {
			return err
		}
		// Gated candidates flow through the unified write jobs (design
		// Decision 9). The terminal outcome is persisted ONLY after this
		// enqueue succeeds: an enqueue failure keeps the stored result
		// resumable (chunks present, empty outcome), so the retry re-enters
		// this block, skips the completed chunks with zero model calls,
		// re-gates and re-enqueues. The (owner_id, idempotency_key) unique
		// key with ON CONFLICT DO NOTHING makes the re-enqueue exactly-once.
		if enqueueErr := w.enqueueExtractionWrites(ctx, job, accepted); enqueueErr != nil {
			return enqueueErr
		}
		if result.Outcome == "" {
			if len(accepted) == 0 {
				result.Outcome = durableExtractionOutcomeNoOutput
			} else {
				result.Outcome = durableExtractionOutcomeExtracted
			}
			if job.ResultJSON, err = json.Marshal(result); err != nil {
				return err
			}
		}
	}
	job.Status = string(memory.ExtractionCompleted)
	job.ErrorMessage = ""
	job.CompletedAt = durablePtrTime(time.Now().UTC())
	job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
	if err := w.updateJob(ctx, job, leaseOwner); err != nil {
		return err
	}
	// The Phase 1 lease is released by the terminal UpdateOwned above. Phase 2
	// retry bookkeeping is deliberately a normal update on the completed row.
	leaseOwner = ""
	if err := w.consolidate(ctx, job.OwnerID); err != nil {
		// Phase 1 is already durably complete. Keep ResultJSON and the completed
		// status so a consolidation retry never re-runs extraction.
		job.ErrorMessage = "phase2:" + err.Error()
		job.Phase2AttemptCount++
		job.DueAt = durablePtrTime(time.Now().UTC().Add(durablePhase2RetryDelay(job.Phase2AttemptCount)))
		_ = w.updateJob(context.WithoutCancel(ctx), job, leaseOwner)
		return err
	}
	job.ErrorMessage = ""
	return nil
}

func (w *DurableMemoryWorker) claim(ctx context.Context, payload DurableMemoryPayload) (*memory.ExtractionJob, bool, error) {
	if payload.JobID <= 0 {
		return nil, false, errors.New("durable memory job_id is required")
	}
	if leased, ok := w.jobs.(memory.ExtractionLeaseRepository); ok {
		return leased.ClaimByID(ctx, payload.OwnerID, payload.JobID, w.workerID, time.Now().UTC().Add(durableStage1Lease))
	}
	job, err := w.jobs.FindByID(ctx, payload.OwnerID, payload.JobID)
	if err != nil {
		return nil, false, err
	}
	if job.Status != string(memory.ExtractionPending) && job.Status != string(memory.ExtractionRunning) {
		return job, false, nil
	}
	job.Status = string(memory.ExtractionRunning)
	job.AttemptCount++
	return job, true, w.jobs.Update(ctx, job)
}

func (w *DurableMemoryWorker) updateJob(ctx context.Context, job *memory.ExtractionJob, leaseOwner ...string) error {
	owner := ""
	if len(leaseOwner) > 0 {
		owner = strings.TrimSpace(leaseOwner[0])
	}
	if leased, ok := w.jobs.(memory.ExtractionLeaseRepository); ok && owner != "" {
		return leased.UpdateOwned(ctx, job, owner)
	}
	return w.jobs.Update(ctx, job)
}

func (w *DurableMemoryWorker) heartbeatStage1Lease(ctx context.Context, leased memory.ExtractionLeaseRepository, jobID int64) {
	if leased == nil || jobID <= 0 || strings.TrimSpace(w.workerID) == "" {
		return
	}
	ticker := time.NewTicker(durableStage1Lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			until := time.Now().UTC().Add(durableStage1Lease)
			if err := leased.RenewLease(ctx, jobID, w.workerID, until); err != nil {
				return
			}
		}
	}
}

// ExtractionCandidate is one structured memory candidate emitted by the
// extraction model (design Decision 7). Candidates are parsed strictly: a
// missing field is a parse error, never a silent zero.
type ExtractionCandidate struct {
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	Type         string   `json:"type"`
	Confidence   float64  `json:"confidence"`
	Importance   float64  `json:"importance"`
	EvidenceRefs []string `json:"evidence_refs"`
}

const (
	// durableExtractionOutcomeExtracted marks a finished chunked extraction;
	// the gate pass (Task 6) may still rewrite the outcome to no_output.
	durableExtractionOutcomeExtracted = "extracted"
	// durableExtractionOutcomeNoOutput marks jobs whose evidence produced no
	// candidates; they complete without writing memories.
	durableExtractionOutcomeNoOutput = "no_output"
)

// durableExtractionResult is the result_json schema for chunked extraction
// (design Decision 8): chunks maps chunk index to its candidates, merge is
// the merge-pass slot, outcome records the terminal Phase-1 state.
// window_after/window_through are additive resume markers: they record the
// evidence window the partial chunks were chunked from. Chunk indices are
// positional within the plan recomputed from that window, so a retry whose
// window moved must discard the partial chunks instead of reusing them.
type durableExtractionResult struct {
	Chunks        map[int][]ExtractionCandidate `json:"chunks"`
	Merge         []ExtractionCandidate         `json:"merge"`
	Outcome       string                        `json:"outcome"`
	WindowAfter   int64                         `json:"window_after"`
	WindowThrough int64                         `json:"window_through"`
	// Rejections records the candidates the quality gate dropped, each with
	// its reason (design Decision 7). Additive: pre-gate payloads omit it.
	Rejections []candidateGateRejection `json:"rejections,omitempty"`
}

// gateCandidates returns the candidate stream the quality gate consumes: the
// merge-pass output when the merge slot is filled (Task 7), otherwise the
// per-chunk candidates flattened in ascending chunk-index order. A single
// chunk therefore goes straight through the gate without a merge call.
func (r durableExtractionResult) gateCandidates() []ExtractionCandidate {
	if len(r.Merge) > 0 {
		return r.Merge
	}
	if len(r.Chunks) == 0 {
		return nil
	}
	indices := make([]int, 0, len(r.Chunks))
	for index := range r.Chunks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	var candidates []ExtractionCandidate
	for _, index := range indices {
		candidates = append(candidates, r.Chunks[index]...)
	}
	return candidates
}

// gateExtractionResult gates the merged-or-single-chunk candidate stream and
// records every rejection in the result (design Decision 7). It deliberately
// does NOT touch the outcome: the terminal outcome is persisted only after
// the accepted candidates have been enqueued, so an enqueue failure leaves
// the stored result resumable (chunks present, outcome empty) and the retry
// re-gates and re-enqueues instead of skipping straight to completed.
func gateExtractionResult(result *durableExtractionResult) []ExtractionCandidate {
	accepted, rejections := gateExtractionCandidates(result.gateCandidates())
	result.Rejections = rejections
	return accepted
}

// decodeDurableExtractionResult classifies a stored result_json. resumable is
// true only for the chunked schema without a terminal outcome; legacy
// pre-chunking payloads and terminal results are both final and must never be
// re-extracted.
func decodeDurableExtractionResult(raw json.RawMessage) (durableExtractionResult, bool) {
	result := durableExtractionResult{Chunks: map[int][]ExtractionCandidate{}}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return result, false
	}
	var probe struct {
		Chunks json.RawMessage `json:"chunks"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil || len(probe.Chunks) == 0 {
		return result, false
	}
	if err := json.Unmarshal(trimmed, &result); err != nil || result.Chunks == nil {
		return durableExtractionResult{Chunks: map[int][]ExtractionCandidate{}}, false
	}
	return result, true
}

// extractChunks runs the per-chunk candidate extraction with incremental
// persistence (design Decision 8): each completed chunk's candidates are
// written into the job's result_json immediately, so a crash or retry skips
// finished chunks and re-sends only the missing ones. Chunk indices are
// positional within the plan, so the caller must have validated the result's
// window markers against the window the plan was chunked from (Handle
// discards partials whose window moved).
func (w *DurableMemoryWorker) extractChunks(ctx context.Context, job *memory.ExtractionJob, result *durableExtractionResult, chunks []EvidenceChunk, leaseOwner string) error {
	if len(chunks) == 0 {
		// Nothing rendered as evidence: no model call, no text dump.
		result.Outcome = durableExtractionOutcomeNoOutput
		return nil
	}
	// Decision 10: a missing model FAILS the extraction into the linear
	// backoff; the retired raw-text dump never runs on this path.
	if w.chatClient == nil || strings.TrimSpace(w.cfg.Model) == "" {
		return errors.New("durable memory extraction requires a configured model")
	}
	for _, chunk := range chunks {
		if _, done := result.Chunks[chunk.Index]; done {
			continue
		}
		candidates, err := w.extractChunkCandidates(ctx, chunk)
		if err != nil {
			return err
		}
		result.Chunks[chunk.Index] = candidates
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return marshalErr
		}
		job.ResultJSON = encoded
		if updateErr := w.updateJob(ctx, job, leaseOwner); updateErr != nil {
			return updateErr
		}
	}
	// The terminal extracted outcome is NOT set here: Handle persists it only
	// after the accepted candidates have been enqueued, so an enqueue failure
	// keeps the stored result resumable and no candidates are ever lost.
	return nil
}

// extractChunkCandidates asks the extraction model for structured candidates
// over one chunk of rendered evidence. Unit payloads were redacted by the
// renderer; the assembled evidence is redacted again as defense in depth so
// no raw secret reaches the model prompt.
func (w *DurableMemoryWorker) extractChunkCandidates(ctx context.Context, chunk EvidenceChunk) ([]ExtractionCandidate, error) {
	var evidence strings.Builder
	for _, unit := range chunk.Units {
		evidence.WriteString(evidenceChunkedText(unit))
		evidence.WriteByte('\n')
	}
	prompt := "Extract durable, reusable memories from this conversation evidence. Do not invent facts. Every candidate must cite evidence from this chunk. Return JSON only: {\"candidates\":[{\"title\":\"...\",\"content\":\"...\",\"type\":\"lesson|preference|fact|constraint\",\"confidence\":0.0,\"importance\":0.0,\"evidence_refs\":[\"messages:<from>-<to>\",\"tool_call:<id>\"]}]}. Return an empty candidates array when nothing is durable.\n\nEVIDENCE:\n" + redactDurableSecrets(evidence.String())
	response, err := w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return nil, err
	}
	return parseExtractionCandidates(extractDurableJSON(response.Content))
}

// rawExtractionCandidate uses pointer fields so a missing key is
// distinguishable from a zero value; materializing then refuses the candidate
// instead of silently zeroing it.
type rawExtractionCandidate struct {
	Title        *string   `json:"title"`
	Content      *string   `json:"content"`
	Type         *string   `json:"type"`
	Confidence   *float64  `json:"confidence"`
	Importance   *float64  `json:"importance"`
	EvidenceRefs *[]string `json:"evidence_refs"`
}

func parseExtractionCandidates(raw string) ([]ExtractionCandidate, error) {
	var envelope struct {
		Candidates *[]rawExtractionCandidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("durable extraction returned invalid JSON: %w", err)
	}
	if envelope.Candidates == nil {
		return nil, errors.New(`durable extraction response is missing the "candidates" array`)
	}
	candidates := make([]ExtractionCandidate, 0, len(*envelope.Candidates))
	for index, rawCandidate := range *envelope.Candidates {
		candidate, err := materializeExtractionCandidate(rawCandidate)
		if err != nil {
			return nil, fmt.Errorf("durable extraction candidate %d: %w", index, err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func materializeExtractionCandidate(raw rawExtractionCandidate) (ExtractionCandidate, error) {
	candidate := ExtractionCandidate{}
	if raw.Title == nil {
		return candidate, errors.New(`missing field "title"`)
	}
	if raw.Content == nil {
		return candidate, errors.New(`missing field "content"`)
	}
	if raw.Type == nil {
		return candidate, errors.New(`missing field "type"`)
	}
	if raw.Confidence == nil {
		return candidate, errors.New(`missing field "confidence"`)
	}
	if raw.Importance == nil {
		return candidate, errors.New(`missing field "importance"`)
	}
	if raw.EvidenceRefs == nil {
		return candidate, errors.New(`missing field "evidence_refs"`)
	}
	candidate.Title = strings.TrimSpace(*raw.Title)
	candidate.Content = strings.TrimSpace(*raw.Content)
	candidate.Type = strings.TrimSpace(*raw.Type)
	candidate.Confidence = *raw.Confidence
	candidate.Importance = *raw.Importance
	candidate.EvidenceRefs = *raw.EvidenceRefs
	return candidate, nil
}

// Deterministic quality gates for extraction candidates (design Decision 7).
// The thresholds use >= semantics; the gate is a pure function so the merge
// pass (Task 7) re-gates merged output through the same path.
const (
	durableGateMinConfidence = 0.7
	durableGateMinImportance = 0.5
)

// candidateGateRejection records one dropped candidate in result_json so the
// gate decision stays observable: every rejection carries a reason.
type candidateGateRejection struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// gateExtractionCandidates applies the deterministic quality gates to a
// candidate stream and returns the accepted candidates plus one recorded
// reason per dropped candidate.
func gateExtractionCandidates(candidates []ExtractionCandidate) ([]ExtractionCandidate, []candidateGateRejection) {
	accepted := make([]ExtractionCandidate, 0, len(candidates))
	var rejections []candidateGateRejection
	for index, candidate := range candidates {
		if reason := gateExtractionCandidate(candidate); reason != "" {
			rejections = append(rejections, candidateGateRejection{Index: index, Title: candidate.Title, Reason: reason})
			continue
		}
		accepted = append(accepted, candidate)
	}
	return accepted, rejections
}

func gateExtractionCandidate(candidate ExtractionCandidate) string {
	if reason := gateExtractionScore("confidence", candidate.Confidence); reason != "" {
		return reason
	}
	if reason := gateExtractionScore("importance", candidate.Importance); reason != "" {
		return reason
	}
	if candidate.Confidence < durableGateMinConfidence {
		return fmt.Sprintf("confidence %.2f below minimum %.2f", candidate.Confidence, durableGateMinConfidence)
	}
	if candidate.Importance < durableGateMinImportance {
		return fmt.Sprintf("importance %.2f below minimum %.2f", candidate.Importance, durableGateMinImportance)
	}
	if strings.TrimSpace(candidate.Title) == "" {
		return "title is blank"
	}
	if strings.TrimSpace(candidate.Content) == "" {
		return "content is blank"
	}
	if !hasEvidenceRef(candidate.EvidenceRefs) {
		return "evidence references are missing"
	}
	if redactedCandidateContent(candidate.Content) == "" {
		return "content is empty after secret redaction"
	}
	return ""
}

func gateExtractionScore(name string, value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Sprintf("%s is not a finite score in [0,1]", name)
	}
	return ""
}

func hasEvidenceRef(refs []string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			return true
		}
	}
	return false
}

// durableRedactionPlaceholder matches every placeholder redactDurableSecrets
// can emit; removing them leaves the durable content a redacted candidate
// still carries.
var durableRedactionPlaceholder = regexp.MustCompile(`\[REDACTED(?: PRIVATE KEY)?\]`)

// redactedCandidateContent is the usable content remaining after secret
// redaction: a candidate whose content is entirely secret material leaves
// nothing durable and fails the gate.
func redactedCandidateContent(content string) string {
	redacted := redactDurableSecrets(content)
	return strings.TrimSpace(durableRedactionPlaceholder.ReplaceAllString(redacted, " "))
}

// previousBoundary is the window start: the through of the conversation's
// latest completed durable job (MAX(id), not MAX(through)). Completion order
// is not message order — a newer boundary may finish before an older job — so
// the caller still treats any returned boundary at or beyond the current
// through as already covered (the out-of-order shadow rule).
func (w *DurableMemoryWorker) previousBoundary(ctx context.Context, current *memory.ExtractionJob) int64 {
	if current == nil || current.ThroughMessageID <= 0 {
		return 0
	}
	boundary, err := w.jobs.LatestCompletedDurableThrough(ctx, current.OwnerID, current.ConversationID)
	if err != nil {
		return 0
	}
	return boundary
}

// messagesThrough reads the extraction window. Repositories with the
// archive-inclusive range read take it (archived rows are durable evidence);
// everything else falls back to the historical active-only reads unchanged.
func (w *DurableMemoryWorker) messagesThrough(ctx context.Context, ownerID, conversationID, after, through int64) ([]conversation.Message, error) {
	if archived, ok := w.messages.(dreamMessageArchiveRangeReader); ok {
		return archived.ListThroughIncludingArchived(ctx, ownerID, conversationID, after, through)
	}
	if ranged, ok := w.messages.(dreamMessageRangeReader); ok {
		return ranged.ListActiveAfterThrough(ctx, ownerID, conversationID, after, through)
	}
	items, err := w.messages.ListActiveThrough(ctx, ownerID, conversationID, through)
	if err != nil {
		return nil, err
	}
	filtered := items[:0]
	for _, item := range items {
		if item.ID > after {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (w *DurableMemoryWorker) conversationLock(ctx context.Context, ownerID, conversationID int64) (func(), error) {
	if w.redis == nil {
		key := fmt.Sprintf("%d:%d", ownerID, conversationID)
		value, _ := durableConversationFallbackLocks.LoadOrStore(key, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		return lock.Unlock, nil
	}
	key := fmt.Sprintf("durable:memory:conversation:%d:%d", ownerID, conversationID)
	token := uuid.NewString()
	ok, err := w.redis.SetNX(ctx, key, token, durableStage1Lease).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("durable memory conversation is already being extracted")
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	go renewDurableRedisLease(leaseCtx, w.redis, key, token, durableStage1Lease)
	return func() {
		cancel()
		_, _ = w.redis.Eval(context.Background(), `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`, []string{key}, token).Result()
	}, nil
}

// consolidate is the Phase-2 entry point. It gathers the durable evidence
// memory rows (ad_hoc and extraction), then delegates to the SQL
// consolidation projection, which computes the version diff, invokes the
// consolidation agent at most once and persists two versioned artifacts.
// Any failure returned here keeps the job retryable through the caller's
// Phase-2 retry bookkeeping; no filesystem fallback exists.
func (w *DurableMemoryWorker) consolidate(ctx context.Context, ownerID int64) error {
	unlock, err := w.phase2Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	inputs, err := w.gatherConsolidationInputs(ctx, ownerID)
	if err != nil {
		return err
	}
	if len(inputs) == 0 {
		// Nothing to consolidate: no LLM call and no artifact mutation.
		return nil
	}
	if w.projection == nil {
		return fmt.Errorf("durable memory consolidation projection is not configured")
	}
	_, _, err = w.projection.Project(ctx, ownerID, inputs, w)
	return err
}

// gatherConsolidationInputs selects the owner's durable evidence MEMORY ROWS
// (source ad_hoc and extraction) as Phase-2 evidence. SourceID in every
// ProjectionInput is the row's memories.id — the canonical mapping shared by
// citations (Task 5), owner validation (Task 6) and lifecycle
// protection/removal (Task 7). Durable extraction evidence with no
// corresponding memory row is excluded: the extraction->memory write wiring
// gap belongs to the extraction producer (Task 2) and the Task 8 migration.
func (w *DurableMemoryWorker) gatherConsolidationInputs(ctx context.Context, ownerID int64) ([]ProjectionInput, error) {
	if w.sources == nil {
		return nil, nil
	}
	rows, err := w.sources.ListBySources(ctx, ownerID, []string{"ad_hoc", "extraction"}, 0)
	if err != nil {
		return nil, err
	}
	inputs := make([]ProjectionInput, 0, len(rows))
	for _, row := range rows {
		var kind string
		switch row.Source {
		case "ad_hoc":
			kind = ConsolidationSourceAdHoc
		case "extraction":
			kind = ConsolidationSourceRollout
		default:
			continue
		}
		content := strings.TrimSpace(redactDurableSecrets(row.Content))
		if content == "" {
			continue
		}
		var conversationID int64
		if row.SourceConversationID != nil {
			conversationID = *row.SourceConversationID
		}
		inputs = append(inputs, ProjectionInput{
			SourceRef:      ProjectionSourceRef{SourceID: row.ID, Kind: kind, ConversationID: conversationID},
			RawMemory:      content,
			RolloutSummary: "",
			SourceAt:       consolidationSourceAt(row),
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].SourceRef.SourceID < inputs[j].SourceRef.SourceID })
	return inputs, nil
}

// consolidationSourceAt is the SourceAt mapping for consolidation evidence:
// COALESCE(last_used_at, updated_at, created_at) of the memory row.
func consolidationSourceAt(item memory.Memory) time.Time {
	if item.LastUsedAt != nil && !item.LastUsedAt.IsZero() {
		return *item.LastUsedAt
	}
	if !item.UpdatedAt.IsZero() {
		return item.UpdatedAt
	}
	return item.CreatedAt
}

// Consolidate implements ConsolidationAgent. The version diff is always
// presented before the existing artifacts and the new raw input.
func (w *DurableMemoryWorker) Consolidate(ctx context.Context, ownerID int64, diff ConsolidationDiff, currentMemory, currentSummary, raw string) (string, string, error) {
	if w.chatClient == nil || strings.TrimSpace(w.cfg.Model) == "" {
		return "", "", errors.New("durable memory consolidation requires a configured model")
	}
	prompt := "You are the single internal memory consolidation agent. Read the workspace diff first. Consolidate into one durable, non-duplicated handbook. Preserve supported facts, merge duplicates, remove stale contradictions, and keep provenance from rollout evidence. Return JSON only: {\"memory\":\"MEMORY.md markdown\",\"summary\":\"compact routing summary\"}. Do not include v1 in summary and do not emit taxonomy, scopes, tiers, or promotion instructions.\n\nWORKSPACE DIFF (read first):\n" + diff.RenderDiff() + "\nEXISTING MEMORY:\n" + currentMemory + "\n\nEXISTING SUMMARY:\n" + currentSummary + "\n\nNEW RAW INPUT:\n" + raw
	response, err := w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return "", "", err
	}
	var result durableConsolidationResult
	if err := json.Unmarshal([]byte(extractDurableJSON(response.Content)), &result); err != nil {
		return "", "", err
	}
	result.Memory = strings.TrimSpace(result.Memory)
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Memory == "" {
		result.Memory = raw
	}
	if result.Summary == "" {
		return "", "", errors.New("durable memory consolidation returned an empty summary")
	}
	return result.Memory, result.Summary, nil
}

func (w *DurableMemoryWorker) phase2Lock(ctx context.Context) (func(), error) {
	if w.redis == nil {
		if !durablePhase2FallbackLock.TryLock() {
			return nil, fmt.Errorf("durable memory phase2 is already running")
		}
		return durablePhase2FallbackLock.Unlock, nil
	}
	key, token := "durable:memory:phase2", uuid.NewString()
	ok, err := w.redis.SetNX(ctx, key, token, durablePhase2Lease).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("durable memory phase2 is already running")
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	go renewDurableRedisLease(leaseCtx, w.redis, key, token, durablePhase2Lease)
	return func() {
		cancel()
		_, _ = w.redis.Eval(context.Background(), `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`, []string{key}, token).Result()
	}, nil
}

func renewDurableRedisLease(ctx context.Context, client *redis.Client, key, token string, ttl time.Duration) {
	if client == nil || ttl <= 0 {
		return
	}
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := client.Eval(ctx, `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) else return 0 end`, []string{key}, token, fmt.Sprint(ttl.Milliseconds())).Result()
			if err != nil {
				return
			}
		}
	}
}

type durableConsolidationResult struct {
	Memory  string `json:"memory"`
	Summary string `json:"summary"`
}

func latestDurableMessageID(ctx context.Context, messages DreamMessageRepository, ownerID, conversationID int64) (int64, error) {
	if reader, ok := messages.(dreamMessageBoundaryReader); ok {
		return reader.LatestActiveMessageID(ctx, ownerID, conversationID)
	}
	items, err := messages.ListActiveByConversation(ctx, ownerID, conversationID)
	if err != nil || len(items) == 0 {
		return 0, err
	}
	return items[len(items)-1].ID, nil
}

func durablePtrTime(value time.Time) *time.Time { return &value }

func durablePhase2RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := time.Minute * time.Duration(1<<uint(attempt-1))
	if delay > durablePhase2MaxBackoff {
		return durablePhase2MaxBackoff
	}
	return delay
}

func (w *DurableMemoryWorker) deferPhase2Retry(ctx context.Context, job *memory.ExtractionJob, cause error) error {
	if job == nil || w == nil || w.jobs == nil {
		return nil
	}
	job.Phase2AttemptCount++
	job.ErrorMessage = "phase2:" + cause.Error()
	job.DueAt = durablePtrTime(time.Now().UTC().Add(durablePhase2RetryDelay(job.Phase2AttemptCount)))
	return w.jobs.Update(ctx, job)
}

var durableSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token)\s*[:=]\s*[^\s,;]{8,}`),
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._-]{12,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----`),
}

func redactDurableSecrets(value string) string {
	value = durableSecretPatterns[0].ReplaceAllStringFunc(value, func(match string) string {
		key := strings.TrimSpace(match)
		if colon := strings.IndexAny(key, ":="); colon > 0 {
			return key[:colon+1] + "[REDACTED]"
		}
		return "[REDACTED]"
	})
	value = durableSecretPatterns[1].ReplaceAllString(value, "bearer [REDACTED]")
	return durableSecretPatterns[2].ReplaceAllString(value, "[REDACTED PRIVATE KEY]")
}

func summarizeDurableText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) > 1200 {
		value = string([]rune(value)[:1200]) + "..."
	}
	return value
}

func extractDurableJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			return raw[start : end+1]
		}
	}
	return raw
}
