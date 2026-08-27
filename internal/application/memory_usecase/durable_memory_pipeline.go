package memory_usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// DurableStage1Result is the three-field boundary between extraction and
// consolidation. The result is stored on the claimed extraction row before Phase 2.
type DurableStage1Result struct {
	RawMemory      string `json:"raw_memory"`
	RolloutSummary string `json:"rollout_summary"`
	RolloutSlug    string `json:"rollout_slug,omitempty"`
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
}

// ConsolidationSourceReader lists durable evidence memory rows so Phase-2
// consolidation consumes the canonical memory rows instead of job rows. The
// returned rows are identified by memories.id: artifact source refs MUST carry
// the memory ID, never a write-job or durable-job ID.
type ConsolidationSourceReader interface {
	ListBySources(ctx context.Context, ownerID int64, sources []string, limit int) ([]memory.Memory, error)
}

func WithConsolidationProjection(projection *ConsolidationProjection) func(*DurableMemoryWorkerOptions) {
	return func(options *DurableMemoryWorkerOptions) { options.Projection = projection }
}

func WithConsolidationSources(reader ConsolidationSourceReader) func(*DurableMemoryWorkerOptions) {
	return func(options *DurableMemoryWorkerOptions) { options.Sources = reader }
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
}

func NewDurableMemoryWorker(chatClient llm.ChatClient, messages DreamMessageRepository, jobs memory.ExtractionJobRepository, redisClient *redis.Client, cfg DurableMemoryConfig, workerID string, optionFns ...func(*DurableMemoryWorkerOptions)) *DurableMemoryWorker {
	options := &DurableMemoryWorkerOptions{}
	for _, apply := range optionFns {
		if apply != nil {
			apply(options)
		}
	}
	return &DurableMemoryWorker{chatClient: chatClient, messages: messages, jobs: jobs, redis: redisClient, cfg: cfg, workerID: workerID, projection: options.Projection, sources: options.Sources}
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
	// call the extraction model a second time.
	if len(bytes.TrimSpace(job.ResultJSON)) == 0 {
		previousThrough := w.previousBoundary(ctx, job)
		if previousThrough >= job.ThroughMessageID && job.ThroughMessageID > 0 {
			job.ResultJSON = json.RawMessage(`{"raw_memory":"","rollout_summary":""}`)
		} else {
			messages, messageErr := w.messagesThrough(ctx, job.OwnerID, job.ConversationID, previousThrough, job.ThroughMessageID)
			if messageErr != nil {
				return messageErr
			}
			if len(messages) > 0 {
				result, extractErr := w.extract(ctx, messages)
				if extractErr != nil {
					return extractErr
				}
				job.ResultJSON, err = json.Marshal(result)
				if err != nil {
					return err
				}
			} else {
				job.ResultJSON = json.RawMessage(`{"raw_memory":"","rollout_summary":""}`)
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

func (w *DurableMemoryWorker) extract(ctx context.Context, messages []conversation.Message) (DurableStage1Result, error) {
	var text strings.Builder
	for _, item := range messages {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		text.WriteString(item.Role)
		text.WriteString(": ")
		text.WriteString(content)
		text.WriteByte('\n')
		if text.Len() >= durableMaxRolloutLen {
			break
		}
	}
	if text.Len() == 0 {
		return DurableStage1Result{}, nil
	}
	if w.chatClient == nil || w.cfg.Model == "" {
		return DurableStage1Result{RawMemory: redactDurableSecrets(text.String()), RolloutSummary: summarizeDurableText(text.String())}, nil
	}
	prompt := "Extract durable, reusable memory from this completed rollout. Do not invent facts. Return JSON only: {\"raw_memory\":\"...\",\"rollout_summary\":\"...\",\"rollout_slug\":\"lowercase-slug\"}. If nothing is durable, return empty strings.\n\nROLLOUT:\n" + text.String()
	response, err := w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return DurableStage1Result{}, err
	}
	var result DurableStage1Result
	if err := json.Unmarshal([]byte(extractDurableJSON(response.Content)), &result); err != nil {
		return DurableStage1Result{}, err
	}
	result.RawMemory = redactDurableSecrets(strings.TrimSpace(result.RawMemory))
	result.RolloutSummary = redactDurableSecrets(strings.TrimSpace(result.RolloutSummary))
	result.RolloutSlug = safeDurableSlug(result.RolloutSlug)
	return result, nil
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

func (w *DurableMemoryWorker) messagesThrough(ctx context.Context, ownerID, conversationID, after, through int64) ([]conversation.Message, error) {
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
	if w.chatClient == nil || w.cfg.Model == "" {
		return raw, summarizeDurableText(raw), nil
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
		result.Summary = summarizeDurableText(result.Memory)
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

func safeDurableSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
		} else if builder.Len() > 0 {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
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
