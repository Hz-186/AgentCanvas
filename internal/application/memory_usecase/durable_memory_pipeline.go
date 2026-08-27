package memory_usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// DurableMemoryJobType is the only production memory-generation job. Candidate
// proposals and retention-tier schedulers are deliberately not part of this
// pipeline: a rollout is extracted once, then one consolidation writer owns
// the durable artifacts.
const DurableMemoryJobType = "memory:durable"

const (
	durableStage1Lease           = 2 * time.Minute
	durablePhase2Lease           = 10 * time.Minute
	durableMaxRolloutLen         = 120_000
	durablePhase2MaxBackoff      = 6 * time.Hour
	durableWorkspaceDiffFile     = "phase2_workspace_diff.md"
	durableWorkspaceManifestFile = ".phase2.baseline.json"
	durableConsumedWatermarkFile = ".phase2.consumed_watermark"
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

type durableStatusAfterIDReader interface {
	ListByStatusAfterID(ctx context.Context, ownerID int64, status string, afterID int64, limit int) ([]memory.ExtractionJob, error)
}

type durableAdHocInput struct {
	Path    string
	Content string
}

type durableRolloutInput struct {
	Job    memory.ExtractionJob
	Result DurableStage1Result
}

type durableConsolidationResult struct {
	Memory  string `json:"memory"`
	Summary string `json:"summary"`
}

type DurableMemoryWorker struct {
	chatClient llm.ChatClient
	messages   DreamMessageRepository
	jobs       memory.ExtractionJobRepository
	redis      *redis.Client
	cfg        DurableMemoryConfig
	workerID   string
}

func NewDurableMemoryWorker(chatClient llm.ChatClient, messages DreamMessageRepository, jobs memory.ExtractionJobRepository, redisClient *redis.Client, cfg DurableMemoryConfig, workerID string) *DurableMemoryWorker {
	return &DurableMemoryWorker{chatClient: chatClient, messages: messages, jobs: jobs, redis: redisClient, cfg: cfg, workerID: workerID}
}

// NewDurableMemoryTrigger schedules one job per stable conversation boundary.
// Redis only coalesces bursts; the extraction idempotency key is the durable
// exactly-once guard.
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
		// Scope burst coalescing to this exact source boundary. A conversation
		// level key would suppress a later boundary and lose messages.
		pendingKey := fmt.Sprintf("durable:pending:%d:%d:%d", ownerID, conversationID, through)
		if redisClient != nil {
			ttl := cfg.IdleTimeout
			if ttl <= 0 {
				ttl = time.Minute
			}
			if ok, setErr := redisClient.SetNX(ctx, pendingKey, 1, ttl).Result(); setErr != nil || !ok {
				return
			}
		}
		key := fmt.Sprintf("durable:%d:%d:%d", ownerID, conversationID, through)
		// Check before creating so a repeated terminal event does not enqueue the
		// same durable extraction row again. The unique database key remains the
		// final race-safe guard when two callers arrive concurrently.
		if existing, findErr := jobs.FindByIdempotencyKey(ctx, ownerID, key); findErr == nil && existing != nil {
			return
		}
		job := &memory.ExtractionJob{
			BaseModel:        domain.BaseModel{OwnerID: ownerID},
			ConversationID:   conversationID,
			ThroughMessageID: through,
			IdempotencyKey:   key,
			TriggerReason:    "durable",
			Status:           string(memory.ExtractionPending),
			DueAt:            durablePtrTime(time.Now().UTC().Add(cfg.IdleTimeout)),
		}
		created := true
		if err := jobs.Create(ctx, job); err != nil {
			created = false
			job, err = jobs.FindByIdempotencyKey(ctx, ownerID, key)
			if err != nil {
				if redisClient != nil {
					_, _ = redisClient.Del(context.Background(), pendingKey).Result()
				}
				return
			}
		}
		if jobQueue == nil {
			return
		}
		if !created {
			return
		}
		queueJob := queueinfra.Job{SchemaVersion: queueinfra.JobSchemaVersion, ID: fmt.Sprintf("durable-job-%d", job.ID), Type: DurableMemoryJobType, Payload: map[string]any{"job_id": job.ID, "owner_id": ownerID, "conversation_id": conversationID}, AvailableAt: *job.DueAt}
		if err := jobQueue.Publish(ctx, queueJob); err != nil && redisClient != nil {
			_, _ = redisClient.Del(context.Background(), pendingKey).Result()
		}
	}
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

func (w *DurableMemoryWorker) previousBoundary(ctx context.Context, current *memory.ExtractionJob) int64 {
	if current == nil || current.ThroughMessageID <= 0 {
		return 0
	}
	items, err := w.jobs.ListByStatus(ctx, current.OwnerID, string(memory.ExtractionCompleted), 200)
	if err != nil {
		return 0
	}
	var boundary int64
	for _, item := range items {
		if item.TriggerReason != "durable" || item.ID == current.ID || item.ConversationID != current.ConversationID {
			continue
		}
		// Completion order is not message order: a newer boundary may finish
		// before an older job. Treat any completed boundary at or beyond this
		// one as already covered, independent of database IDs.
		if item.ThroughMessageID >= current.ThroughMessageID {
			return item.ThroughMessageID
		}
		if item.ThroughMessageID > boundary {
			boundary = item.ThroughMessageID
		}
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

func (w *DurableMemoryWorker) consolidate(ctx context.Context, ownerID int64) error {
	unlock, err := w.phase2Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	jobs, err := listCompletedDurableJobs(ctx, w.jobs, ownerID)
	if err != nil {
		return err
	}
	inputs := make([]durableRolloutInput, 0, len(jobs))
	for _, job := range jobs {
		if job.TriggerReason != "durable" || len(job.ResultJSON) == 0 {
			continue
		}
		var result DurableStage1Result
		if json.Unmarshal(job.ResultJSON, &result) == nil && (result.RawMemory != "" || result.RolloutSummary != "") {
			inputs = append(inputs, durableRolloutInput{Job: job, Result: result})
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Job.ID < inputs[j].Job.ID })
	root := filepath.Join(w.cfg.Root, fmt.Sprintf("owner-%d", ownerID))
	if _, err := ensureDurableDirectory(root, true, "rollout_summaries"); err != nil {
		return err
	}
	notes, err := readDurableAdHocInputs(ctx, root)
	if err != nil {
		return err
	}
	digest := durableInputDigest(inputs, notes)
	// The pipeline initializes an empty workspace without a model call. Once a
	// baseline exists, however, deletions are workspace changes too and must go
	// through the one consolidation writer rather than silently resetting it.
	if len(inputs) == 0 && len(notes) == 0 {
		if _, err := os.Stat(filepath.Join(root, durableWorkspaceManifestFile)); os.IsNotExist(err) {
			return ensureDurableEmptyArtifacts(root, digest)
		} else if err != nil {
			return err
		}
	}
	raw := renderDurableRawMemories(inputs, notes)
	if err := writeDurableAtomic(filepath.Join(root, "raw_memories.md"), raw); err != nil {
		return err
	}
	if err := syncDurableRolloutSummaries(root, inputs); err != nil {
		return err
	}
	currentManifest, err := buildDurableWorkspaceManifest(root)
	if err != nil {
		return err
	}
	baseline, err := readDurableWorkspaceManifest(filepath.Join(root, durableWorkspaceManifestFile))
	if err != nil {
		return err
	}
	changes := durableWorkspaceChanges(baseline, currentManifest)
	if len(changes) == 0 && durableArtifactsExist(root) {
		return nil
	}
	if err := writeDurableWorkspaceDiff(root, changes); err != nil {
		return err
	}
	currentMemory, _ := os.ReadFile(filepath.Join(root, "MEMORY.md"))
	currentSummary, _ := os.ReadFile(filepath.Join(root, "memory_summary.md"))
	diffBytes, _ := os.ReadFile(filepath.Join(root, durableWorkspaceDiffFile))
	result, err := w.runConsolidation(ctx, string(currentMemory), string(currentSummary), raw, string(diffBytes))
	if err != nil {
		return err
	}
	if err := writeDurableConsolidatedArtifacts(root, string(currentMemory), string(currentSummary), result); err != nil {
		return err
	}
	finalManifest, err := buildDurableWorkspaceManifest(root)
	if err != nil {
		return err
	}
	if err := writeDurableWorkspaceManifest(filepath.Join(root, durableWorkspaceManifestFile), finalManifest); err != nil {
		return err
	}
	if err := writeDurableAtomic(filepath.Join(root, ".phase2.sha256"), durableManifestDigest(finalManifest, digest)+"\n"); err != nil {
		return err
	}
	return writeDurableAtomic(filepath.Join(root, durableConsumedWatermarkFile), fmt.Sprintf("%d\n", maxDurableJobID(inputs)))
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

func (w *DurableMemoryWorker) runConsolidation(ctx context.Context, currentMemory, currentSummary, raw, workspaceDiff string) (durableConsolidationResult, error) {
	if w.chatClient == nil || w.cfg.Model == "" {
		return durableConsolidationResult{Memory: raw, Summary: summarizeDurableText(raw)}, nil
	}
	prompt := "You are the single internal memory consolidation agent. Read the workspace diff first. Consolidate into one durable, non-duplicated handbook. Preserve supported facts, merge duplicates, remove stale contradictions, and keep provenance from rollout evidence. Return JSON only: {\"memory\":\"MEMORY.md markdown\",\"summary\":\"compact routing summary\"}. Do not include v1 in summary and do not emit taxonomy, scopes, tiers, or promotion instructions.\n\nWORKSPACE DIFF (read first):\n" + workspaceDiff + "\n\nEXISTING MEMORY:\n" + currentMemory + "\n\nEXISTING SUMMARY:\n" + currentSummary + "\n\nNEW RAW INPUT:\n" + raw
	response, err := w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return durableConsolidationResult{}, err
	}
	var result durableConsolidationResult
	if err := json.Unmarshal([]byte(extractDurableJSON(response.Content)), &result); err != nil {
		return durableConsolidationResult{}, err
	}
	result.Memory = strings.TrimSpace(result.Memory)
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Memory == "" {
		result.Memory = raw
	}
	if result.Summary == "" {
		result.Summary = summarizeDurableText(result.Memory)
	}
	return result, nil
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

func listCompletedDurableJobs(ctx context.Context, jobs memory.ExtractionJobRepository, ownerID int64) ([]memory.ExtractionJob, error) {
	if reader, ok := jobs.(durableStatusAfterIDReader); ok {
		const pageSize = 500
		all := make([]memory.ExtractionJob, 0, pageSize)
		var afterID int64
		for {
			page, err := reader.ListByStatusAfterID(ctx, ownerID, string(memory.ExtractionCompleted), afterID, pageSize)
			if err != nil {
				return nil, err
			}
			if len(page) == 0 {
				break
			}
			all = append(all, page...)
			lastID := page[len(page)-1].ID
			if lastID <= afterID {
				return nil, fmt.Errorf("completed durable-memory job pagination did not advance")
			}
			afterID = lastID
			if len(page) < pageSize {
				break
			}
		}
		return all, nil
	}
	// Test doubles and old repositories expose only the legacy bounded method.
	// Production uses the keyset path above; the fallback keeps those callers
	// source-compatible during the migration window.
	return jobs.ListByStatus(ctx, ownerID, string(memory.ExtractionCompleted), 256)
}

func durablePtrTime(value time.Time) *time.Time { return &value }

func durableInputDigest(inputs []durableRolloutInput, notes []durableAdHocInput) string {
	hash := sha256.New()
	for _, input := range inputs {
		fmt.Fprintf(hash, "%d\x00%d\x00%s\x00%s\x00%s\n", input.Job.ID, input.Job.ThroughMessageID, input.Result.RawMemory, input.Result.RolloutSummary, input.Result.RolloutSlug)
	}
	for _, note := range notes {
		fmt.Fprintf(hash, "note\x00%s\x00%s\n", note.Path, note.Content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// durableWorkspaceManifest is the small, explicit baseline used instead of
// treating a digest of only the input rows as the workspace state. It includes
// every markdown artifact that Phase 2 may read or write, while excluding
// locks, claims, and the generated diff itself.
type durableWorkspaceManifest map[string]string

type durableWorkspaceChange struct {
	Status string
	Path   string
	Before string
	After  string
}

func buildDurableWorkspaceManifest(root string) (durableWorkspaceManifest, error) {
	manifest := durableWorkspaceManifest{}
	for _, name := range []string{"MEMORY.md", "memory_summary.md", "raw_memories.md"} {
		if err := addDurableManifestFile(root, name, manifest); err != nil {
			return nil, err
		}
	}
	for _, dir := range []string{"rollout_summaries", "skills", filepath.Join("extensions", "ad_hoc", "notes")} {
		base := filepath.Join(root, dir)
		walkErr := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if entry.IsDir() {
				if path != base && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".md" {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			return addDurableManifestFile(root, filepath.ToSlash(rel), manifest)
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return manifest, nil
}

func addDurableManifestFile(root, rel string, manifest durableWorkspaceManifest) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("durable memory artifact must not be a symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	manifest[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
	return nil
}

func readDurableWorkspaceManifest(path string) (durableWorkspaceManifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return durableWorkspaceManifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest durableWorkspaceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode durable memory baseline: %w", err)
	}
	if manifest == nil {
		manifest = durableWorkspaceManifest{}
	}
	return manifest, nil
}

func writeDurableWorkspaceManifest(path string, manifest durableWorkspaceManifest) error {
	if manifest == nil {
		manifest = durableWorkspaceManifest{}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeDurableAtomic(path, string(data)+"\n")
}

func durableWorkspaceChanges(previous, current durableWorkspaceManifest) []durableWorkspaceChange {
	paths := make(map[string]struct{}, len(previous)+len(current))
	for path := range previous {
		paths[path] = struct{}{}
	}
	for path := range current {
		paths[path] = struct{}{}
	}
	changes := make([]durableWorkspaceChange, 0, len(paths))
	for path := range paths {
		before, hadBefore := previous[path]
		after, hadAfter := current[path]
		switch {
		case !hadBefore && hadAfter:
			changes = append(changes, durableWorkspaceChange{Status: "added", Path: path, After: after})
		case hadBefore && !hadAfter:
			changes = append(changes, durableWorkspaceChange{Status: "deleted", Path: path, Before: before})
		case before != after:
			changes = append(changes, durableWorkspaceChange{Status: "modified", Path: path, Before: before, After: after})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func writeDurableWorkspaceDiff(root string, changes []durableWorkspaceChange) error {
	var b strings.Builder
	b.WriteString("# Memory Workspace Diff\n\n")
	b.WriteString("Generated before Phase 2. Read this file first; it is system input and must not be edited.\n\n")
	if len(changes) == 0 {
		b.WriteString("No workspace changes.\n")
	} else {
		b.WriteString("## Changes\n\n")
		for _, change := range changes {
			fmt.Fprintf(&b, "- %s: `%s`\n", change.Status, change.Path)
		}
		b.WriteString("\n## Content hashes\n\n")
		for _, change := range changes {
			fmt.Fprintf(&b, "- `%s` before=%s after=%s\n", change.Path, emptyHash(change.Before), emptyHash(change.After))
		}
	}
	return writeDurableAtomic(filepath.Join(root, durableWorkspaceDiffFile), b.String())
}

func emptyHash(value string) string {
	if value == "" {
		return "<missing>"
	}
	return value
}

func durableManifestDigest(manifest durableWorkspaceManifest, inputDigest string) string {
	data, _ := json.Marshal(manifest)
	hash := sha256.New()
	_, _ = hash.Write(data)
	fmt.Fprintf(hash, "\x00%s", inputDigest)
	return hex.EncodeToString(hash.Sum(nil))
}

func maxDurableJobID(inputs []durableRolloutInput) int64 {
	var maxID int64
	for _, input := range inputs {
		if input.Job.ID > maxID {
			maxID = input.Job.ID
		}
	}
	return maxID
}

func renderDurableRawMemories(inputs []durableRolloutInput, notes []durableAdHocInput) string {
	if len(inputs) == 0 && len(notes) == 0 {
		return "# Raw Memories\n\nNo durable memory was extracted.\n"
	}
	var builder strings.Builder
	builder.WriteString("# Raw Memories\n\n")
	for _, input := range inputs {
		builder.WriteString(fmt.Sprintf("## Rollout %d\n\n", input.Job.ID))
		if input.Result.RawMemory != "" {
			builder.WriteString(input.Result.RawMemory)
			builder.WriteString("\n\n")
		}
		if input.Result.RolloutSummary != "" {
			builder.WriteString("Summary: ")
			builder.WriteString(input.Result.RolloutSummary)
			builder.WriteString("\n\n")
		}
	}
	for _, note := range notes {
		builder.WriteString("## Ad-hoc note ")
		builder.WriteString(note.Path)
		builder.WriteString("\n\n[ad-hoc note]\n")
		builder.WriteString(note.Content)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func syncDurableRolloutSummaries(root string, inputs []durableRolloutInput) error {
	dir := filepath.Join(root, "rollout_summaries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Evidence is an audit trail. Upsert the summaries represented by this
	// batch, but never clear older rollouts merely because the selection window
	// is bounded.
	for _, input := range inputs {
		slug := safeDurableSlug(input.Result.RolloutSlug)
		if slug == "" {
			slug = fmt.Sprintf("rollout-%d", input.Job.ID)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, input.Job.ID))
		content := fmt.Sprintf("# Rollout %d\n\n%s\n", input.Job.ID, input.Result.RolloutSummary)
		if err := writeDurableAtomic(path, content); err != nil {
			return err
		}
	}
	return nil
}

func durableArtifactsExist(root string) bool {
	for _, name := range []string{"MEMORY.md", "memory_summary.md", "raw_memories.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return false
		}
	}
	return true
}

func readDurableAdHocInputs(ctx context.Context, root string) ([]durableAdHocInput, error) {
	dir := filepath.Join(root, "extensions", "ad_hoc", "notes")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		paths = append(paths, entry.Name())
	}
	sort.Strings(paths)
	inputs := make([]durableAdHocInput, 0, len(paths))
	for _, name := range paths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return nil, readErr
		}
		content := strings.TrimSpace(redactDurableSecrets(string(data)))
		if content == "" {
			continue
		}
		inputs = append(inputs, durableAdHocInput{Path: filepath.ToSlash(filepath.Join("extensions", "ad_hoc", "notes", name)), Content: content})
	}
	return inputs, nil
}

func durableWorkspaceDigest(root, inputDigest string) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "input\x00%s\n", inputDigest)
	paths := []string{"MEMORY.md", "memory_summary.md", "raw_memories.md"}
	for _, dir := range []string{"rollout_summaries", "skills"} {
		base := filepath.Join(root, dir)
		walkErr := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if entry.IsDir() {
				if path != base && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".md" {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if walkErr != nil {
			return "", walkErr
		}
	}
	// Ad-hoc notes are inputs, but include their bytes so a changed note creates
	// a new workspace state even when the extraction rows are unchanged.
	notesDir := filepath.Join(root, "extensions", "ad_hoc", "notes")
	if entries, readErr := os.ReadDir(notesDir); readErr == nil {
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && filepath.Ext(entry.Name()) == ".md" {
				rel := filepath.ToSlash(filepath.Join("extensions", "ad_hoc", "notes", entry.Name()))
				paths = append(paths, rel)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return "", readErr
	}
	sort.Strings(paths)
	seen := make(map[string]struct{}, len(paths))
	for _, rel := range paths {
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if os.IsNotExist(readErr) {
			fmt.Fprintf(hash, "file\x00%s\x00<missing>\n", rel)
			continue
		}
		if readErr != nil {
			return "", readErr
		}
		fmt.Fprintf(hash, "file\x00%s\x00%d\x00", rel, len(data))
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ensureDurableEmptyArtifacts(root, inputDigest string) error {
	for name, content := range map[string]string{
		"raw_memories.md":   "# Raw Memories\n\nNo durable memory was extracted.\n",
		"MEMORY.md":         "# Memory\n",
		"memory_summary.md": "v1\n\nNo durable memory yet.\n",
	} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := writeDurableAtomic(path, content); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	digest, err := durableWorkspaceDigest(root, inputDigest)
	if err != nil {
		return err
	}
	manifest, err := buildDurableWorkspaceManifest(root)
	if err != nil {
		return err
	}
	if err := writeDurableWorkspaceManifest(filepath.Join(root, durableWorkspaceManifestFile), manifest); err != nil {
		return err
	}
	if err := writeDurableAtomic(filepath.Join(root, ".phase2.sha256"), digest+"\n"); err != nil {
		return err
	}
	return writeDurableAtomic(filepath.Join(root, durableConsumedWatermarkFile), "0\n")
}

func writeDurableConsolidatedArtifacts(root, previousMemory, previousSummary string, result durableConsolidationResult) error {
	memoryPath := filepath.Join(root, "MEMORY.md")
	summaryPath := filepath.Join(root, "memory_summary.md")
	if err := writeDurableAtomic(memoryPath, result.Memory); err != nil {
		return err
	}
	if err := writeDurableAtomic(summaryPath, "v1\n\n"+result.Summary); err != nil {
		// Keep the last successful pair intact if the second replacement fails.
		if previousMemory == "" {
			_ = os.Remove(memoryPath)
		} else {
			_ = writeDurableAtomic(memoryPath, previousMemory)
		}
		if previousSummary != "" {
			_ = writeDurableAtomic(summaryPath, previousSummary)
		}
		return err
	}
	return nil
}

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

func writeDurableAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".durable-memory-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
