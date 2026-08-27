package memory_usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

type recordingCodexChatClient struct {
	mu       sync.Mutex
	content  string
	err      error
	requests []llm.ChatRequest
}

func (c *recordingCodexChatClient) Chat(_ context.Context, _ llm.ChatProviderConfig, request llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, request)
	if c.err != nil {
		return nil, c.err
	}
	return &llm.ChatResponse{Content: c.content}, nil
}

func (*recordingCodexChatClient) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

func (c *recordingCodexChatClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func codexTestWorker(root string, chat llm.ChatClient, jobs memory.ExtractionJobRepository, messages DreamMessageRepository) *CodexMemoryWorker {
	return NewCodexMemoryWorker(chat, messages, jobs, nil, CodexMemoryConfig{Enabled: true, Model: "test", Root: root}, "test-worker")
}

type recordingExtractionLeaseRepo struct {
	*fakeExtractionRepo
	ownedUpdates int
	plainUpdates int
}

func (r *recordingExtractionLeaseRepo) ClaimByID(_ context.Context, ownerID, id int64, workerID string, leaseUntil time.Time) (*memory.ExtractionJob, bool, error) {
	job, err := r.fakeExtractionRepo.FindByID(context.Background(), ownerID, id)
	if err != nil {
		return nil, false, err
	}
	if job.Status == string(memory.ExtractionCompleted) || job.Status == string(memory.ExtractionFailed) || job.Status == string(memory.ExtractionSuperseded) {
		return job, false, nil
	}
	now := time.Now().UTC()
	job.Status = string(memory.ExtractionRunning)
	job.AttemptCount++
	job.LockedBy, job.LockedAt, job.LeaseExpiresAt = workerID, &now, &leaseUntil
	r.fakeExtractionRepo.jobs[id] = job
	return job, true, nil
}

func (r *recordingExtractionLeaseRepo) RenewLease(context.Context, int64, string, time.Time) error {
	return nil
}

func (r *recordingExtractionLeaseRepo) UpdateOwned(_ context.Context, job *memory.ExtractionJob, _ string) error {
	r.ownedUpdates++
	clone := *job
	r.fakeExtractionRepo.jobs[job.ID] = &clone
	return nil
}

func (r *recordingExtractionLeaseRepo) Update(ctx context.Context, job *memory.ExtractionJob) error {
	r.plainUpdates++
	return r.fakeExtractionRepo.Update(ctx, job)
}

func TestCodexHandleUsesOwnedUpdateAfterClearingLeaseFields(t *testing.T) {
	jobs := &recordingExtractionLeaseRepo{fakeExtractionRepo: &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: {
		BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, ConversationID: 3, ThroughMessageID: 1,
		TriggerReason: "codex", Status: string(memory.ExtractionPending), ResultJSON: []byte(`{"raw_memory":"stored","rollout_summary":"summary"}`),
	}}}}
	worker := NewCodexMemoryWorker(nil, &fakeDreamMessages{}, jobs, nil, CodexMemoryConfig{Enabled: true, Root: t.TempDir()}, "test-worker")
	if err := worker.Handle(context.Background(), CodexMemoryPayload{JobID: 1, OwnerID: 7, ConversationID: 3}); err != nil {
		t.Fatal(err)
	}
	if jobs.ownedUpdates != 1 || jobs.plainUpdates != 0 {
		t.Fatalf("terminal lease update used wrong path: owned=%d plain=%d", jobs.ownedUpdates, jobs.plainUpdates)
	}
}

func TestCodexConsolidateEmptyInputInitializesWithoutModel(t *testing.T) {
	root := t.TempDir()
	chat := &recordingCodexChatClient{content: `{"memory":"unexpected","summary":"unexpected"}`}
	worker := codexTestWorker(root, chat, &fakeExtractionRepo{}, &fakeDreamMessages{})
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 0 {
		t.Fatalf("empty input called consolidation model %d time(s)", chat.calls())
	}
	ownerRoot := filepath.Join(root, "owner-7")
	for _, name := range []string{"MEMORY.md", "memory_summary.md", "raw_memories.md"} {
		if _, err := os.Stat(filepath.Join(ownerRoot, name)); err != nil {
			t.Fatalf("missing empty-workspace artifact %s: %v", name, err)
		}
	}
}

func TestCodexConsolidateConsumesAdHocNotesExactlyOnce(t *testing.T) {
	root := t.TempDir()
	ownerRoot := filepath.Join(root, "owner-7", "extensions", "ad_hoc", "notes")
	if err := os.MkdirAll(ownerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	note := "# Ad-hoc memory note\n\n- source_run_id: 11\n\nRemember the user prefers concise Chinese replies."
	if err := os.WriteFile(filepath.Join(ownerRoot, "20260101T000000.000000000Z-note.md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	chat := &recordingCodexChatClient{content: `{"memory":"# Memory\n\nconcise Chinese replies","summary":"concise replies"}`}
	worker := codexTestWorker(root, chat, &fakeExtractionRepo{}, &fakeDreamMessages{})
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 1 || !strings.Contains(chat.requests[0].Messages[0].Content, "[ad-hoc note]") {
		t.Fatalf("ad-hoc note was not supplied to consolidation: calls=%d requests=%+v", chat.calls(), chat.requests)
	}
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 1 {
		t.Fatalf("unchanged ad-hoc input reran consolidation: calls=%d", chat.calls())
	}
}

func TestCodexConsolidateFailurePreservesPreviousBaseline(t *testing.T) {
	root := t.TempDir()
	ownerRoot := filepath.Join(root, "owner-7")
	if err := os.MkdirAll(ownerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerRoot, "MEMORY.md"), []byte("old memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerRoot, "memory_summary.md"), []byte("v1\n\nold summary"), 0o600); err != nil {
		t.Fatal(err)
	}
	jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: {
		BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, ConversationID: 3, TriggerReason: "codex", Status: string(memory.ExtractionCompleted),
		ResultJSON: []byte(`{"raw_memory":"new fact","rollout_summary":"new rollout"}`),
	}}}
	worker := codexTestWorker(root, &recordingCodexChatClient{err: errors.New("model unavailable")}, jobs, &fakeDreamMessages{})
	if err := worker.consolidate(context.Background(), 7); err == nil {
		t.Fatal("expected consolidation error")
	}
	memoryFile, _ := os.ReadFile(filepath.Join(ownerRoot, "MEMORY.md"))
	summaryFile, _ := os.ReadFile(filepath.Join(ownerRoot, "memory_summary.md"))
	if string(memoryFile) != "old memory" || string(summaryFile) != "v1\n\nold summary" {
		t.Fatalf("baseline changed after failed consolidation: memory=%q summary=%q", memoryFile, summaryFile)
	}
}

func TestCodexHandleRetryUsesStoredStageOneResult(t *testing.T) {
	root := t.TempDir()
	chat := &recordingCodexChatClient{err: errors.New("phase2 unavailable")}
	jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: {
		BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, ConversationID: 3, ThroughMessageID: 1, TriggerReason: "codex", Status: string(memory.ExtractionPending),
		ResultJSON: []byte(`{"raw_memory":"already extracted","rollout_summary":"summary"}`),
	}}}
	worker := codexTestWorker(root, chat, jobs, &fakeDreamMessages{items: []conversation.Message{{ImmutableModel: domain.ImmutableModel{ID: 1}, Content: "must not re-extract"}}})
	if err := worker.Handle(context.Background(), CodexMemoryPayload{JobID: 1, OwnerID: 7, ConversationID: 3}); err == nil {
		t.Fatal("expected phase2 error")
	}
	job := jobs.jobs[1]
	if job.Status != string(memory.ExtractionCompleted) || job.Phase2AttemptCount != 1 || job.DueAt == nil || !strings.HasPrefix(job.ErrorMessage, "phase2:") {
		t.Fatalf("phase2 retry state = %+v", job)
	}
	if chat.calls() != 1 {
		t.Fatalf("stored stage-one result caused %d model calls, want only phase2", chat.calls())
	}
}

func TestCodexPhase2FallbackLockRejectsConcurrentWorker(t *testing.T) {
	first := &CodexMemoryWorker{}
	second := &CodexMemoryWorker{}
	unlock, err := first.phase2Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := second.phase2Lock(context.Background()); err == nil {
		t.Fatal("concurrent phase2 worker acquired fallback lock")
	}
}

func TestCodexPhase2RetryDelayBacksOff(t *testing.T) {
	if got, want := codexPhase2RetryDelay(1), time.Minute; got != want {
		t.Fatalf("first retry delay=%s want=%s", got, want)
	}
	if got, want := codexPhase2RetryDelay(4), 8*time.Minute; got != want {
		t.Fatalf("fourth retry delay=%s want=%s", got, want)
	}
}
