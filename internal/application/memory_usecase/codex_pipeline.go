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

// CodexMemoryJobType is the only production memory-generation job. Candidate
// proposals and retention-tier schedulers are deliberately not part of this
// pipeline: a rollout is extracted once, then one consolidation writer owns
// the durable artifacts.
const CodexMemoryJobType = "memory:codex"

const (
	codexStage1Lease           = 2 * time.Minute
	codexPhase2Lease           = 10 * time.Minute
	codexMaxRolloutLen         = 120_000
	codexPhase2MaxBackoff      = 6 * time.Hour
	codexWorkspaceDiffFile     = "phase2_workspace_diff.md"
	codexWorkspaceManifestFile = ".phase2.baseline.json"
	codexConsumedWatermarkFile = ".phase2.consumed_watermark"
)

// ponytail: one process-local lock is the smallest safe fallback when Redis
// is unavailable; deployments that need cross-process coordination must keep
// Redis enabled (the normal production path).
var codexPhase2FallbackLock sync.Mutex
var codexConversationFallbackLocks sync.Map

type CodexMemoryConfig struct {
	Enabled     bool
	IdleTimeout time.Duration
	Provider    llm.ChatProviderConfig
	Model       string
	Root        string
}

func NewCodexMemoryConfig(cfg config.MemoryDreamConfig) CodexMemoryConfig {
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
	return CodexMemoryConfig{
		Enabled:     cfg.Enabled,
		IdleTimeout: idle,
		Provider:    llm.ChatProviderConfig{ProviderType: cfg.LLMProviderType, BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey},
		Model:       strings.TrimSpace(cfg.LLMModel),
		Root:        root,
	}
}

type CodexMemoryPayload struct {
	JobID          int64 `json:"job_id"`
	OwnerID        int64 `json:"owner_id"`
	ConversationID int64 `json:"conversation_id"`
}

// CodexStage1Result is intentionally the same three-field boundary used by
// Codex. The result is stored on the claimed extraction row before Phase 2.
type CodexStage1Result struct {
	RawMemory      string `json:"raw_memory"`
	RolloutSummary string `json:"rollout_summary"`
	RolloutSlug    string `json:"rollout_slug,omitempty"`
}

type codexPhase2RetryReader interface {
	ListPhase2Retries(ctx context.Context, limit int) ([]memory.ExtractionJob, error)
}

type codexStatusAfterIDReader interface {
	ListByStatusAfterID(ctx context.Context, ownerID int64, status string, afterID int64, limit int) ([]memory.ExtractionJob, error)
}

type codexAdHocInput struct {
	Path    string
	Content string
}

type codexRolloutInput struct {
	Job    memory.ExtractionJob
	Result CodexStage1Result
}

type codexConsolidationResult struct {
	Memory  string `json:"memory"`
	Summary string `json:"summary"`
}

type CodexMemoryWorker struct {
	chatClient llm.ChatClient
	messages   DreamMessageRepository
	jobs       memory.ExtractionJobRepository
	redis      *redis.Client
	cfg        CodexMemoryConfig
	workerID   string
}

func NewCodexMemoryWorker(chatClient llm.ChatClient, messages DreamMessageRepository, jobs memory.ExtractionJobRepository, redisClient *redis.Client, cfg CodexMemoryConfig, workerID string) *CodexMemoryWorker {
	return &CodexMemoryWorker{chatClient: chatClient, messages: messages, jobs: jobs, redis: redisClient, cfg: cfg, workerID: workerID}
}

// NewCodexMemoryTrigger schedules one job per stable conversation boundary.
// Redis only coalesces bursts; the extraction idempotency key is the durable
// exactly-once guard.
func NewCodexMemoryTrigger(jobQueue queueinfra.JobQueue, redisClient *redis.Client, cfg CodexMemoryConfig, jobs memory.ExtractionJobRepository, messages DreamMessageRepository) func(context.Context, int64, int64, int) {
	if !cfg.Enabled || jobs == nil || messages == nil {
		return nil
	}
	return func(ctx context.Context, ownerID, conversationID int64, _ int) {
		if ownerID <= 0 || conversationID <= 0 {
			return
		}
		through, err := latestCodexMessageID(ctx, messages, ownerID, conversationID)
		if err != nil || through <= 0 {
			return
		}
		// Scope burst coalescing to this exact source boundary. A conversation
		// level key would suppress a later boundary and lose messages.
		pendingKey := fmt.Sprintf("codex:pending:%d:%d:%d", ownerID, conversationID, through)
		if redisClient != nil {
			ttl := cfg.IdleTimeout
			if ttl <= 0 {
				ttl = time.Minute
			}
			if ok, setErr := redisClient.SetNX(ctx, pendingKey, 1, ttl).Result(); setErr != nil || !ok {
				return
			}
		}
		key := fmt.Sprintf("codex:%d:%d:%d", ownerID, conversationID, through)
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
			TriggerReason:    "codex",
			Status:           string(memory.ExtractionPending),
			DueAt:            codexPtrTime(time.Now().UTC().Add(cfg.IdleTimeout)),
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
		queueJob := queueinfra.Job{SchemaVersion: queueinfra.JobSchemaVersion, ID: fmt.Sprintf("codex-job-%d", job.ID), Type: CodexMemoryJobType, Payload: map[string]any{"job_id": job.ID, "owner_id": ownerID, "conversation_id": conversationID}, AvailableAt: *job.DueAt}
		if err := jobQueue.Publish(ctx, queueJob); err != nil && redisClient != nil {
			_, _ = redisClient.Del(context.Background(), pendingKey).Result()
		}
	}
}

func (w *CodexMemoryWorker) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.jobs == nil || !w.cfg.Enabled {
		return false, nil
	}
	jobs, err := w.jobs.ListPending(ctx, 50)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.TriggerReason != "codex" {
			continue
		}
		return true, w.Handle(ctx, CodexMemoryPayload{JobID: job.ID, OwnerID: job.OwnerID, ConversationID: job.ConversationID})
	}
	if retries, ok := w.jobs.(codexPhase2RetryReader); ok {
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

func (w *CodexMemoryWorker) Handle(ctx context.Context, payload CodexMemoryPayload) (retErr error) {
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
		job.DueAt = codexPtrTime(time.Now().UTC().Add(time.Duration(job.AttemptCount+1) * time.Minute))
		job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
		_ = w.updateJob(context.WithoutCancel(ctx), job, leaseOwner)
	}()

	if job.DueAt != nil && job.DueAt.After(time.Now().UTC()) {
		job.Status = string(memory.ExtractionPending)
		job.LockedBy, job.LockedAt, job.LeaseExpiresAt = "", nil, nil
		return w.updateJob(ctx, job, leaseOwner)
	}
	if job.ThroughMessageID <= 0 {
		job.ThroughMessageID, err = latestCodexMessageID(ctx, w.messages, job.OwnerID, job.ConversationID)
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
	job.CompletedAt = codexPtrTime(time.Now().UTC())
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
		job.DueAt = codexPtrTime(time.Now().UTC().Add(codexPhase2RetryDelay(job.Phase2AttemptCount)))
		_ = w.updateJob(context.WithoutCancel(ctx), job, leaseOwner)
		return err
	}
	job.ErrorMessage = ""
	return nil
}

func (w *CodexMemoryWorker) claim(ctx context.Context, payload CodexMemoryPayload) (*memory.ExtractionJob, bool, error) {
	if payload.JobID <= 0 {
		return nil, false, errors.New("codex memory job_id is required")
	}
	if leased, ok := w.jobs.(memory.ExtractionLeaseRepository); ok {
		return leased.ClaimByID(ctx, payload.OwnerID, payload.JobID, w.workerID, time.Now().UTC().Add(codexStage1Lease))
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

func (w *CodexMemoryWorker) updateJob(ctx context.Context, job *memory.ExtractionJob, leaseOwner ...string) error {
	owner := ""
	if len(leaseOwner) > 0 {
		owner = strings.TrimSpace(leaseOwner[0])
	}
	if leased, ok := w.jobs.(memory.ExtractionLeaseRepository); ok && owner != "" {
		return leased.UpdateOwned(ctx, job, owner)
	}
	return w.jobs.Update(ctx, job)
}

func (w *CodexMemoryWorker) heartbeatStage1Lease(ctx context.Context, leased memory.ExtractionLeaseRepository, jobID int64) {
	if leased == nil || jobID <= 0 || strings.TrimSpace(w.workerID) == "" {
		return
	}
	ticker := time.NewTicker(codexStage1Lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			until := time.Now().UTC().Add(codexStage1Lease)
			if err := leased.RenewLease(ctx, jobID, w.workerID, until); err != nil {
				return
			}
		}
	}
}

func (w *CodexMemoryWorker) extract(ctx context.Context, messages []conversation.Message) (CodexStage1Result, error) {
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
		if text.Len() >= codexMaxRolloutLen {
			break
		}
	}
	if text.Len() == 0 {
		return CodexStage1Result{}, nil
	}
	if w.chatClient == nil || w.cfg.Model == "" {
		return CodexStage1Result{RawMemory: redactCodexSecrets(text.String()), RolloutSummary: summarizeCodexText(text.String())}, nil
	}
	prompt := "Extract durable, reusable memory from this completed rollout. Do not invent facts. Return JSON only: {\"raw_memory\":\"...\",\"rollout_summary\":\"...\",\"rollout_slug\":\"lowercase-slug\"}. If nothing is durable, return empty strings.\n\nROLLOUT:\n" + text.String()
	response, err := w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return CodexStage1Result{}, err
	}
	var result CodexStage1Result
	if err := json.Unmarshal([]byte(extractCodexJSON(response.Content)), &result); err != nil {
		return CodexStage1Result{}, err
	}
	result.RawMemory = redactCodexSecrets(strings.TrimSpace(result.RawMemory))
	result.RolloutSummary = redactCodexSecrets(strings.TrimSpace(result.RolloutSummary))
	result.RolloutSlug = safeCodexSlug(result.RolloutSlug)
	return result, nil
}

func (w *CodexMemoryWorker) previousBoundary(ctx context.Context, current *memory.ExtractionJob) int64 {
	if current == nil || current.ThroughMessageID <= 0 {
		return 0
	}
	items, err := w.jobs.ListByStatus(ctx, current.OwnerID, string(memory.ExtractionCompleted), 200)
	if err != nil {
		return 0
	}
	var boundary int64
	for _, item := range items {
		if item.TriggerReason != "codex" || item.ID == current.ID || item.ConversationID != current.ConversationID {
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

func (w *CodexMemoryWorker) messagesThrough(ctx context.Context, ownerID, conversationID, after, through int64) ([]conversation.Message, error) {
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

func (w *CodexMemoryWorker) conversationLock(ctx context.Context, ownerID, conversationID int64) (func(), error) {
	if w.redis == nil {
		key := fmt.Sprintf("%d:%d", ownerID, conversationID)
		value, _ := codexConversationFallbackLocks.LoadOrStore(key, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		return lock.Unlock, nil
	}
	key := fmt.Sprintf("codex:memory:conversation:%d:%d", ownerID, conversationID)
	token := uuid.NewString()
	ok, err := w.redis.SetNX(ctx, key, token, codexStage1Lease).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("codex memory conversation is already being extracted")
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	go renewCodexRedisLease(leaseCtx, w.redis, key, token, codexStage1Lease)
	return func() {
		cancel()
		_, _ = w.redis.Eval(context.Background(), `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`, []string{key}, token).Result()
	}, nil
}

func (w *CodexMemoryWorker) consolidate(ctx context.Context, ownerID int64) error {
	unlock, err := w.phase2Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	jobs, err := listCompletedCodexJobs(ctx, w.jobs, ownerID)
	if err != nil {
		return err
	}
	inputs := make([]codexRolloutInput, 0, len(jobs))
	for _, job := range jobs {
		if job.TriggerReason != "codex" || len(job.ResultJSON) == 0 {
			continue
		}
		var result CodexStage1Result
		if json.Unmarshal(job.ResultJSON, &result) == nil && (result.RawMemory != "" || result.RolloutSummary != "") {
			inputs = append(inputs, codexRolloutInput{Job: job, Result: result})
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Job.ID < inputs[j].Job.ID })
	root := filepath.Join(w.cfg.Root, fmt.Sprintf("owner-%d", ownerID))
	if _, err := ensureCodexDirectory(root, true, "rollout_summaries"); err != nil {
		return err
	}
	notes, err := readCodexAdHocInputs(ctx, root)
	if err != nil {
		return err
	}
	digest := codexInputDigest(inputs, notes)
	// Codex initializes an empty workspace without a model call. Once a
	// baseline exists, however, deletions are workspace changes too and must go
	// through the one consolidation writer rather than silently resetting it.
	if len(inputs) == 0 && len(notes) == 0 {
		if _, err := os.Stat(filepath.Join(root, codexWorkspaceManifestFile)); os.IsNotExist(err) {
			return ensureCodexEmptyArtifacts(root, digest)
		} else if err != nil {
			return err
		}
	}
	raw := renderCodexRawMemories(inputs, notes)
	if err := writeCodexAtomic(filepath.Join(root, "raw_memories.md"), raw); err != nil {
		return err
	}
	if err := syncCodexRolloutSummaries(root, inputs); err != nil {
		return err
	}
	currentManifest, err := buildCodexWorkspaceManifest(root)
	if err != nil {
		return err
	}
	baseline, err := readCodexWorkspaceManifest(filepath.Join(root, codexWorkspaceManifestFile))
	if err != nil {
		return err
	}
	changes := codexWorkspaceChanges(baseline, currentManifest)
	if len(changes) == 0 && codexArtifactsExist(root) {
		return nil
	}
	if err := writeCodexWorkspaceDiff(root, changes); err != nil {
		return err
	}
	currentMemory, _ := os.ReadFile(filepath.Join(root, "MEMORY.md"))
	currentSummary, _ := os.ReadFile(filepath.Join(root, "memory_summary.md"))
	diffBytes, _ := os.ReadFile(filepath.Join(root, codexWorkspaceDiffFile))
	result, err := w.runConsolidation(ctx, string(currentMemory), string(currentSummary), raw, string(diffBytes))
	if err != nil {
		return err
	}
	if err := writeCodexConsolidatedArtifacts(root, string(currentMemory), string(currentSummary), result); err != nil {
		return err
	}
	finalManifest, err := buildCodexWorkspaceManifest(root)
	if err != nil {
		return err
	}
	if err := writeCodexWorkspaceManifest(filepath.Join(root, codexWorkspaceManifestFile), finalManifest); err != nil {
		return err
	}
	if err := writeCodexAtomic(filepath.Join(root, ".phase2.sha256"), codexManifestDigest(finalManifest, digest)+"\n"); err != nil {
		return err
	}
	return writeCodexAtomic(filepath.Join(root, codexConsumedWatermarkFile), fmt.Sprintf("%d\n", maxCodexJobID(inputs)))
}

func (w *CodexMemoryWorker) phase2Lock(ctx context.Context) (func(), error) {
	if w.redis == nil {
		if !codexPhase2FallbackLock.TryLock() {
			return nil, fmt.Errorf("codex memory phase2 is already running")
		}
		return codexPhase2FallbackLock.Unlock, nil
	}
	key, token := "codex:memory:phase2", uuid.NewString()
	ok, err := w.redis.SetNX(ctx, key, token, codexPhase2Lease).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("codex memory phase2 is already running")
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	go renewCodexRedisLease(leaseCtx, w.redis, key, token, codexPhase2Lease)
	return func() {
		cancel()
		_, _ = w.redis.Eval(context.Background(), `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`, []string{key}, token).Result()
	}, nil
}

func renewCodexRedisLease(ctx context.Context, client *redis.Client, key, token string, ttl time.Duration) {
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

func (w *CodexMemoryWorker) runConsolidation(ctx context.Context, currentMemory, currentSummary, raw, workspaceDiff string) (codexConsolidationResult, error) {
	if w.chatClient == nil || w.cfg.Model == "" {
		return codexConsolidationResult{Memory: raw, Summary: summarizeCodexText(raw)}, nil
	}
	prompt := "You are the single internal Codex-style memory consolidation agent. Read the workspace diff first. Consolidate into one durable, non-duplicated handbook. Preserve supported facts, merge duplicates, remove stale contradictions, and keep provenance from rollout evidence. Return JSON only: {\"memory\":\"MEMORY.md markdown\",\"summary\":\"compact routing summary\"}. Do not include v1 in summary and do not emit taxonomy, scopes, tiers, or promotion instructions.\n\nWORKSPACE DIFF (read first):\n" + workspaceDiff + "\n\nEXISTING MEMORY:\n" + currentMemory + "\n\nEXISTING SUMMARY:\n" + currentSummary + "\n\nNEW RAW INPUT:\n" + raw
	response, err := w.chatClient.Chat(ctx, w.cfg.Provider, llm.ChatRequest{Model: w.cfg.Model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}})
	if err != nil {
		return codexConsolidationResult{}, err
	}
	var result codexConsolidationResult
	if err := json.Unmarshal([]byte(extractCodexJSON(response.Content)), &result); err != nil {
		return codexConsolidationResult{}, err
	}
	result.Memory = strings.TrimSpace(result.Memory)
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Memory == "" {
		result.Memory = raw
	}
	if result.Summary == "" {
		result.Summary = summarizeCodexText(result.Memory)
	}
	return result, nil
}

func latestCodexMessageID(ctx context.Context, messages DreamMessageRepository, ownerID, conversationID int64) (int64, error) {
	if reader, ok := messages.(dreamMessageBoundaryReader); ok {
		return reader.LatestActiveMessageID(ctx, ownerID, conversationID)
	}
	items, err := messages.ListActiveByConversation(ctx, ownerID, conversationID)
	if err != nil || len(items) == 0 {
		return 0, err
	}
	return items[len(items)-1].ID, nil
}

func listCompletedCodexJobs(ctx context.Context, jobs memory.ExtractionJobRepository, ownerID int64) ([]memory.ExtractionJob, error) {
	if reader, ok := jobs.(codexStatusAfterIDReader); ok {
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
				return nil, fmt.Errorf("completed Codex job pagination did not advance")
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

func codexPtrTime(value time.Time) *time.Time { return &value }

func codexInputDigest(inputs []codexRolloutInput, notes []codexAdHocInput) string {
	hash := sha256.New()
	for _, input := range inputs {
		fmt.Fprintf(hash, "%d\x00%d\x00%s\x00%s\x00%s\n", input.Job.ID, input.Job.ThroughMessageID, input.Result.RawMemory, input.Result.RolloutSummary, input.Result.RolloutSlug)
	}
	for _, note := range notes {
		fmt.Fprintf(hash, "note\x00%s\x00%s\n", note.Path, note.Content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// codexWorkspaceManifest is the small, explicit baseline used instead of
// treating a digest of only the input rows as the workspace state. It includes
// every markdown artifact that Phase 2 may read or write, while excluding
// locks, claims, and the generated diff itself.
type codexWorkspaceManifest map[string]string

type codexWorkspaceChange struct {
	Status string
	Path   string
	Before string
	After  string
}

func buildCodexWorkspaceManifest(root string) (codexWorkspaceManifest, error) {
	manifest := codexWorkspaceManifest{}
	for _, name := range []string{"MEMORY.md", "memory_summary.md", "raw_memories.md"} {
		if err := addCodexManifestFile(root, name, manifest); err != nil {
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
			return addCodexManifestFile(root, filepath.ToSlash(rel), manifest)
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return manifest, nil
}

func addCodexManifestFile(root, rel string, manifest codexWorkspaceManifest) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codex memory artifact must not be a symlink: %s", path)
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

func readCodexWorkspaceManifest(path string) (codexWorkspaceManifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return codexWorkspaceManifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest codexWorkspaceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode codex memory baseline: %w", err)
	}
	if manifest == nil {
		manifest = codexWorkspaceManifest{}
	}
	return manifest, nil
}

func writeCodexWorkspaceManifest(path string, manifest codexWorkspaceManifest) error {
	if manifest == nil {
		manifest = codexWorkspaceManifest{}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeCodexAtomic(path, string(data)+"\n")
}

func codexWorkspaceChanges(previous, current codexWorkspaceManifest) []codexWorkspaceChange {
	paths := make(map[string]struct{}, len(previous)+len(current))
	for path := range previous {
		paths[path] = struct{}{}
	}
	for path := range current {
		paths[path] = struct{}{}
	}
	changes := make([]codexWorkspaceChange, 0, len(paths))
	for path := range paths {
		before, hadBefore := previous[path]
		after, hadAfter := current[path]
		switch {
		case !hadBefore && hadAfter:
			changes = append(changes, codexWorkspaceChange{Status: "added", Path: path, After: after})
		case hadBefore && !hadAfter:
			changes = append(changes, codexWorkspaceChange{Status: "deleted", Path: path, Before: before})
		case before != after:
			changes = append(changes, codexWorkspaceChange{Status: "modified", Path: path, Before: before, After: after})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func writeCodexWorkspaceDiff(root string, changes []codexWorkspaceChange) error {
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
	return writeCodexAtomic(filepath.Join(root, codexWorkspaceDiffFile), b.String())
}

func emptyHash(value string) string {
	if value == "" {
		return "<missing>"
	}
	return value
}

func codexManifestDigest(manifest codexWorkspaceManifest, inputDigest string) string {
	data, _ := json.Marshal(manifest)
	hash := sha256.New()
	_, _ = hash.Write(data)
	fmt.Fprintf(hash, "\x00%s", inputDigest)
	return hex.EncodeToString(hash.Sum(nil))
}

func maxCodexJobID(inputs []codexRolloutInput) int64 {
	var maxID int64
	for _, input := range inputs {
		if input.Job.ID > maxID {
			maxID = input.Job.ID
		}
	}
	return maxID
}

func renderCodexRawMemories(inputs []codexRolloutInput, notes []codexAdHocInput) string {
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

func syncCodexRolloutSummaries(root string, inputs []codexRolloutInput) error {
	dir := filepath.Join(root, "rollout_summaries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Evidence is an audit trail. Upsert the summaries represented by this
	// batch, but never clear older rollouts merely because the selection window
	// is bounded.
	for _, input := range inputs {
		slug := safeCodexSlug(input.Result.RolloutSlug)
		if slug == "" {
			slug = fmt.Sprintf("rollout-%d", input.Job.ID)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, input.Job.ID))
		content := fmt.Sprintf("# Rollout %d\n\n%s\n", input.Job.ID, input.Result.RolloutSummary)
		if err := writeCodexAtomic(path, content); err != nil {
			return err
		}
	}
	return nil
}

func codexArtifactsExist(root string) bool {
	for _, name := range []string{"MEMORY.md", "memory_summary.md", "raw_memories.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return false
		}
	}
	return true
}

func readCodexAdHocInputs(ctx context.Context, root string) ([]codexAdHocInput, error) {
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
	inputs := make([]codexAdHocInput, 0, len(paths))
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
		content := strings.TrimSpace(redactCodexSecrets(string(data)))
		if content == "" {
			continue
		}
		inputs = append(inputs, codexAdHocInput{Path: filepath.ToSlash(filepath.Join("extensions", "ad_hoc", "notes", name)), Content: content})
	}
	return inputs, nil
}

func codexWorkspaceDigest(root, inputDigest string) (string, error) {
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

func ensureCodexEmptyArtifacts(root, inputDigest string) error {
	for name, content := range map[string]string{
		"raw_memories.md":   "# Raw Memories\n\nNo durable memory was extracted.\n",
		"MEMORY.md":         "# Memory\n",
		"memory_summary.md": "v1\n\nNo durable memory yet.\n",
	} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := writeCodexAtomic(path, content); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	digest, err := codexWorkspaceDigest(root, inputDigest)
	if err != nil {
		return err
	}
	manifest, err := buildCodexWorkspaceManifest(root)
	if err != nil {
		return err
	}
	if err := writeCodexWorkspaceManifest(filepath.Join(root, codexWorkspaceManifestFile), manifest); err != nil {
		return err
	}
	if err := writeCodexAtomic(filepath.Join(root, ".phase2.sha256"), digest+"\n"); err != nil {
		return err
	}
	return writeCodexAtomic(filepath.Join(root, codexConsumedWatermarkFile), "0\n")
}

func writeCodexConsolidatedArtifacts(root, previousMemory, previousSummary string, result codexConsolidationResult) error {
	memoryPath := filepath.Join(root, "MEMORY.md")
	summaryPath := filepath.Join(root, "memory_summary.md")
	if err := writeCodexAtomic(memoryPath, result.Memory); err != nil {
		return err
	}
	if err := writeCodexAtomic(summaryPath, "v1\n\n"+result.Summary); err != nil {
		// Keep the last successful pair intact if the second replacement fails.
		if previousMemory == "" {
			_ = os.Remove(memoryPath)
		} else {
			_ = writeCodexAtomic(memoryPath, previousMemory)
		}
		if previousSummary != "" {
			_ = writeCodexAtomic(summaryPath, previousSummary)
		}
		return err
	}
	return nil
}

func codexPhase2RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := time.Minute * time.Duration(1<<uint(attempt-1))
	if delay > codexPhase2MaxBackoff {
		return codexPhase2MaxBackoff
	}
	return delay
}

func (w *CodexMemoryWorker) deferPhase2Retry(ctx context.Context, job *memory.ExtractionJob, cause error) error {
	if job == nil || w == nil || w.jobs == nil {
		return nil
	}
	job.Phase2AttemptCount++
	job.ErrorMessage = "phase2:" + cause.Error()
	job.DueAt = codexPtrTime(time.Now().UTC().Add(codexPhase2RetryDelay(job.Phase2AttemptCount)))
	return w.jobs.Update(ctx, job)
}

func writeCodexAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".codex-memory-*")
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

var codexSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token)\s*[:=]\s*[^\s,;]{8,}`),
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._-]{12,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----`),
}

func redactCodexSecrets(value string) string {
	value = codexSecretPatterns[0].ReplaceAllStringFunc(value, func(match string) string {
		key := strings.TrimSpace(match)
		if colon := strings.IndexAny(key, ":="); colon > 0 {
			return key[:colon+1] + "[REDACTED]"
		}
		return "[REDACTED]"
	})
	value = codexSecretPatterns[1].ReplaceAllString(value, "bearer [REDACTED]")
	return codexSecretPatterns[2].ReplaceAllString(value, "[REDACTED PRIVATE KEY]")
}

func safeCodexSlug(value string) string {
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

func summarizeCodexText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) > 1200 {
		value = string([]rune(value)[:1200]) + "..."
	}
	return value
}

func extractCodexJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			return raw[start : end+1]
		}
	}
	return raw
}
