package memory_usecase

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

var errNotFound = errors.New("record not found")

type fakeExtractionRepo struct {
	jobs    map[int64]*memory.ExtractionJob
	nextID  int64
	created []*memory.ExtractionJob
}

func (r *fakeExtractionRepo) Create(ctx context.Context, job *memory.ExtractionJob) error {
	r.nextID++
	job.ID = r.nextID
	if r.jobs == nil {
		r.jobs = map[int64]*memory.ExtractionJob{}
	}
	clone := *job
	r.jobs[job.ID] = &clone
	r.created = append(r.created, &clone)
	return nil
}

func (r *fakeExtractionRepo) Update(ctx context.Context, job *memory.ExtractionJob) error {
	if r.jobs == nil {
		r.jobs = map[int64]*memory.ExtractionJob{}
	}
	clone := *job
	r.jobs[job.ID] = &clone
	return nil
}

func (r *fakeExtractionRepo) FindByID(ctx context.Context, ownerID, id int64) (*memory.ExtractionJob, error) {
	if r.jobs == nil {
		return nil, errNotFound
	}
	job, ok := r.jobs[id]
	if !ok {
		return nil, errNotFound
	}
	clone := *job
	return &clone, nil
}

func (r *fakeExtractionRepo) FindByIdempotencyKey(ctx context.Context, ownerID int64, key string) (*memory.ExtractionJob, error) {
	for _, job := range r.jobs {
		if job.OwnerID == ownerID && job.IdempotencyKey == key {
			clone := *job
			return &clone, nil
		}
	}
	return nil, errNotFound
}

func (r *fakeExtractionRepo) ListByStatus(ctx context.Context, ownerID int64, status string, limit int) ([]memory.ExtractionJob, error) {
	var result []memory.ExtractionJob
	for _, j := range r.jobs {
		if j.OwnerID == ownerID && j.Status == status {
			result = append(result, *j)
		}
	}
	return result, nil
}

func (r *fakeExtractionRepo) ListPending(ctx context.Context, limit int) ([]memory.ExtractionJob, error) {
	var result []memory.ExtractionJob
	for _, j := range r.jobs {
		if j.Status == string(memory.ExtractionPending) {
			result = append(result, *j)
		}
	}
	return result, nil
}

var _ memory.ExtractionJobRepository = (*fakeExtractionRepo)(nil)

type fakeDreamMessages struct {
	items []conversation.Message
}

func (f *fakeDreamMessages) ListActiveByConversation(context.Context, int64, int64) ([]conversation.Message, error) {
	return append([]conversation.Message(nil), f.items...), nil
}

func (f *fakeDreamMessages) ListActiveThrough(_ context.Context, _, _, throughMessageID int64) ([]conversation.Message, error) {
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if item.ID <= throughMessageID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *fakeDreamMessages) LatestActiveMessageID(_ context.Context, _, _ int64) (int64, error) {
	if len(f.items) == 0 {
		return 0, nil
	}
	return f.items[len(f.items)-1].ID, nil
}

func (f *fakeDreamMessages) ListActiveAfterThrough(_ context.Context, _, _, afterMessageID, throughMessageID int64) ([]conversation.Message, error) {
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if item.ID > afterMessageID && item.ID <= throughMessageID {
			items = append(items, item)
		}
	}
	return items, nil
}

type recordingDurableChatClient struct {
	mu       sync.Mutex
	content  string
	err      error
	requests []llm.ChatRequest
}

func (c *recordingDurableChatClient) Chat(_ context.Context, _ llm.ChatProviderConfig, request llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, request)
	if c.err != nil {
		return nil, c.err
	}
	return &llm.ChatResponse{Content: c.content}, nil
}

func (*recordingDurableChatClient) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

func (c *recordingDurableChatClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// fakeMemorySourceRepo is an in-memory ConsolidationSourceReader returning the
// seeded durable evidence memory rows for the requested owner and sources.
// Rows keep their memories.id so tests can prove artifact refs carry it.
type fakeMemorySourceRepo struct {
	rows []memory.Memory
}

func newFakeMemorySourceRepo(rows ...memory.Memory) *fakeMemorySourceRepo {
	return &fakeMemorySourceRepo{rows: rows}
}

func (r *fakeMemorySourceRepo) ListBySources(_ context.Context, ownerID int64, sources []string, _ int) ([]memory.Memory, error) {
	allowed := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		allowed[source] = struct{}{}
	}
	out := make([]memory.Memory, 0, len(r.rows))
	for _, row := range r.rows {
		if row.OwnerID != ownerID {
			continue
		}
		if _, ok := allowed[row.Source]; !ok {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func consolidationSourceMemory(id int64, source string, content string, conversationID *int64, at time.Time) memory.Memory {
	return memory.Memory{
		SoftDeleteModel:      domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: id, OwnerID: 7, CreatedAt: at.Add(-48 * time.Hour), UpdatedAt: at}},
		SourceConversationID: conversationID,
		Status:               memory.StatusActive,
		Content:              content,
		Source:               source,
	}
}

// durableProjectionTestWorker wires the Phase-2 SQL projection into the
// durable worker with an in-memory artifact repository and memory-row
// evidence sources.
func durableProjectionTestWorker(root string, chat llm.ChatClient, jobs memory.ExtractionJobRepository, messages DreamMessageRepository, artifacts *fakeArtifactRepo, sources *fakeMemorySourceRepo) *DurableMemoryWorker {
	projection := NewConsolidationProjection(artifacts)
	projection.Now = projectionTestNow
	return NewDurableMemoryWorker(
		chat, messages, jobs, nil,
		DurableMemoryConfig{Enabled: true, Model: "test", Root: root},
		"test-worker",
		WithConsolidationProjection(projection),
		WithConsolidationSources(sources),
	)
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

func TestDurableHandleUsesOwnedUpdateAfterClearingLeaseFields(t *testing.T) {
	jobs := &recordingExtractionLeaseRepo{fakeExtractionRepo: &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: {
		BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, ConversationID: 3, ThroughMessageID: 1,
		TriggerReason: "durable", Status: string(memory.ExtractionPending), ResultJSON: []byte(`{"raw_memory":"stored","rollout_summary":"summary"}`),
	}}}}
	artifacts := newFakeArtifactRepo()
	chat := &recordingDurableChatClient{content: `{"memory":"# Memory\n\nstored","summary":"stored"}`}
	worker := durableProjectionTestWorker(t.TempDir(), chat, jobs, &fakeDreamMessages{}, artifacts, newFakeMemorySourceRepo())
	if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: 7, ConversationID: 3}); err != nil {
		t.Fatal(err)
	}
	if jobs.ownedUpdates != 1 || jobs.plainUpdates != 0 {
		t.Fatalf("terminal lease update used wrong path: owned=%d plain=%d", jobs.ownedUpdates, jobs.plainUpdates)
	}
}

func TestDurableConsolidateEmptyInputSkipsProjection(t *testing.T) {
	root := t.TempDir()
	chat := &recordingDurableChatClient{content: `{"memory":"unexpected","summary":"unexpected"}`}
	artifacts := newFakeArtifactRepo()
	worker := durableProjectionTestWorker(root, chat, &fakeExtractionRepo{}, &fakeDreamMessages{}, artifacts, newFakeMemorySourceRepo())
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 0 {
		t.Fatalf("empty input called consolidation model %d time(s)", chat.calls())
	}
	if artifacts.count() != 0 {
		t.Fatalf("empty input wrote %d artifact row(s), want zero", artifacts.count())
	}
}

func TestDurableConsolidateConsumesAdHocNotesExactlyOnce(t *testing.T) {
	root := t.TempDir()
	conversationID := int64(3)
	sources := newFakeMemorySourceRepo(
		consolidationSourceMemory(21, "ad_hoc", "Remember the user prefers concise Chinese replies.", &conversationID, projectionTestNow()),
	)
	artifacts := newFakeArtifactRepo()
	chat := &recordingDurableChatClient{content: `{"memory":"# Memory\n\nconcise Chinese replies","summary":"concise replies"}`}
	worker := durableProjectionTestWorker(root, chat, &fakeExtractionRepo{}, &fakeDreamMessages{}, artifacts, sources)
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 1 || !strings.Contains(chat.requests[0].Messages[0].Content, "[ad-hoc note]") {
		t.Fatalf("ad-hoc note was not supplied to consolidation: calls=%d requests=%+v", chat.calls(), chat.requests)
	}
	if artifacts.count() != 2 {
		t.Fatalf("consolidation wrote %d artifact row(s), want handbook+summary", artifacts.count())
	}
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 1 {
		t.Fatalf("unchanged ad-hoc input reran consolidation: calls=%d", chat.calls())
	}
}

// TestDurableConsolidateSourceRefsCarryMemoryIDs is Fix round 1 for Task 7:
// the canonical mapping decision makes SourceRefsJSON.source_id the memory
// row's memories.id — never a write-job or durable-job ID. Both source kinds
// are mapped from memory rows and the persisted refs carry the memory IDs.
func TestDurableConsolidateSourceRefsCarryMemoryIDs(t *testing.T) {
	root := t.TempDir()
	conversationID := int64(3)
	sources := newFakeMemorySourceRepo(
		consolidationSourceMemory(41, "ad_hoc", "ad-hoc fact from memory row 41", &conversationID, projectionTestNow()),
		consolidationSourceMemory(42, "extraction", "extracted fact from memory row 42", nil, projectionTestNow()),
	)
	artifacts := newFakeArtifactRepo()
	chat := &recordingDurableChatClient{content: `{"memory":"# Memory\n\nfacts","summary":"facts"}`}
	worker := durableProjectionTestWorker(root, chat, &fakeExtractionRepo{}, &fakeDreamMessages{}, artifacts, sources)
	if err := worker.consolidate(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if chat.calls() != 1 {
		t.Fatalf("consolidation model called %d time(s), want 1", chat.calls())
	}
	if artifacts.count() != 2 {
		t.Fatalf("consolidation wrote %d artifact row(s), want handbook+summary", artifacts.count())
	}
	handbook, err := artifacts.Latest(context.Background(), 7, memory.ArtifactKindHandbook)
	if err != nil {
		t.Fatalf("reload handbook artifact: %v", err)
	}
	var refs []ProjectionSourceRef
	if err := decodeProjectionSourceRefs(handbook.SourceRefsJSON, &refs); err != nil {
		t.Fatalf("decode handbook source refs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("handbook source refs=%+v, want the two memory-row refs", refs)
	}
	// Refs are sorted by kind then source id: ad_hoc 41, rollout 42.
	if refs[0].SourceID != 41 || refs[0].Kind != ConsolidationSourceAdHoc || refs[0].ConversationID != 3 {
		t.Fatalf("ad-hoc ref=%+v, want memories.id 41 / kind ad_hoc / conversation 3", refs[0])
	}
	if refs[1].SourceID != 42 || refs[1].Kind != ConsolidationSourceRollout || refs[1].ConversationID != 0 {
		t.Fatalf("extraction ref=%+v, want memories.id 42 / kind rollout / conversation 0", refs[1])
	}
	summary, err := artifacts.Latest(context.Background(), 7, memory.ArtifactKindSummary)
	if err != nil {
		t.Fatalf("reload summary artifact: %v", err)
	}
	var summaryRefs []ProjectionSourceRef
	if err := decodeProjectionSourceRefs(summary.SourceRefsJSON, &summaryRefs); err != nil {
		t.Fatalf("decode summary source refs: %v", err)
	}
	if len(summaryRefs) != 2 || summaryRefs[0].SourceID != 41 || summaryRefs[1].SourceID != 42 {
		t.Fatalf("summary source refs=%+v, want memory IDs 41 and 42", summaryRefs)
	}
	// The agent prompt carried the memory-row content, not job payloads.
	prompt := chat.requests[0].Messages[0].Content
	if !strings.Contains(prompt, "ad-hoc fact from memory row 41") || !strings.Contains(prompt, "extracted fact from memory row 42") {
		t.Fatalf("consolidation prompt lacks memory-row content: %q", prompt)
	}
}

func TestDurableConsolidateFailureKeepsJobRetryableWithoutFiles(t *testing.T) {
	root := t.TempDir()
	artifacts := newFakeArtifactRepo()
	artifacts.failCreate = errors.New("sql transaction failed")
	sources := newFakeMemorySourceRepo(
		consolidationSourceMemory(31, "extraction", "new fact", nil, projectionTestNow()),
	)
	worker := durableProjectionTestWorker(root, &recordingDurableChatClient{content: `{"memory":"# Memory\n\nnew fact","summary":"new fact"}`}, &fakeExtractionRepo{}, &fakeDreamMessages{}, artifacts, sources)
	if err := worker.consolidate(context.Background(), 7); err == nil {
		t.Fatal("expected consolidation artifact write error")
	}
	if artifacts.count() != 0 {
		t.Fatalf("failed artifact transaction persisted %d row(s), want zero", artifacts.count())
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		t.Fatalf("filesystem fallback attempted: %d entries under %s", len(entries), root)
	}
}

func TestDurableHandleRetryUsesStoredStageOneResult(t *testing.T) {
	root := t.TempDir()
	chat := &recordingDurableChatClient{err: errors.New("phase2 unavailable")}
	jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: {
		BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, ConversationID: 3, ThroughMessageID: 1, TriggerReason: "durable", Status: string(memory.ExtractionPending),
		ResultJSON: []byte(`{"raw_memory":"already extracted","rollout_summary":"summary"}`),
	}}}
	// Phase 2 consumes memory rows; a seeded extraction evidence row makes the
	// consolidation actually run and hit the failing chat client.
	sources := newFakeMemorySourceRepo(
		consolidationSourceMemory(41, "extraction", "already extracted", nil, projectionTestNow()),
	)
	worker := durableProjectionTestWorker(root, chat, jobs, &fakeDreamMessages{items: []conversation.Message{{ImmutableModel: domain.ImmutableModel{ID: 1}, Content: "must not re-extract"}}}, newFakeArtifactRepo(), sources)
	if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: 7, ConversationID: 3}); err == nil {
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

func TestDurablePhase2FallbackLockRejectsConcurrentWorker(t *testing.T) {
	first := &DurableMemoryWorker{}
	second := &DurableMemoryWorker{}
	unlock, err := first.phase2Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := second.phase2Lock(context.Background()); err == nil {
		t.Fatal("concurrent phase2 worker acquired fallback lock")
	}
}

func TestDurablePhase2RetryDelayBacksOff(t *testing.T) {
	if got, want := durablePhase2RetryDelay(1), time.Minute; got != want {
		t.Fatalf("first retry delay=%s want=%s", got, want)
	}
	if got, want := durablePhase2RetryDelay(4), 8*time.Minute; got != want {
		t.Fatalf("fourth retry delay=%s want=%s", got, want)
	}
}
