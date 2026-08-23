package memory_usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
)

type fakeExtractionRepo struct {
	jobs    map[int64]*memory.ExtractionJob
	nextID  int64
	created []*memory.ExtractionJob
}

type fakeConversationRepo struct {
	conversation.Repository
	projectID int64
}

func (r fakeConversationRepo) FindByID(context.Context, int64, int64) (*conversation.Conversation, error) {
	return &conversation.Conversation{ProjectID: &r.projectID}, nil
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

func TestExtractionService_CompleteExtractionFailsWhenCreateFails(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	memRepo.created = nil
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo)
	svc.ConfigureCandidates(&fakeCandidateWriter{err: errors.New("candidate failed")})

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	err := svc.CompleteExtraction(context.Background(), jobID, 100, &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{{Content: "will fail", Confidence: 0.9}},
	})
	if err == nil {
		t.Fatal("expected error when memory create fails")
	}
	job, _ := extRepo.FindByID(context.Background(), 100, jobID)
	if job.Status != string(memory.ExtractionFailed) {
		t.Fatalf("expected failed job, got %s", job.Status)
	}
}

func TestExtractionService_CompatibilityAdapterDoesNotMergeDirectly(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	memRepo.Create(context.Background(), &memory.Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: 100}}, MemoryType: memory.TypeProfile, Content: "alpha beta gamma delta epsilon zeta eta theta", Importance: 0.1})
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo)
	candidates := &fakeCandidateWriter{}
	svc.ConfigureCandidates(candidates)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	err := svc.CompleteExtraction(context.Background(), jobID, 100, &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{{Content: "alpha beta gamma delta epsilon zeta eta iota", Confidence: 0.9, Importance: 0.9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, _ := extRepo.FindByID(context.Background(), 100, jobID)
	if job.Status != string(memory.ExtractionCompleted) || len(candidates.items) != 1 {
		t.Fatalf("legacy extraction must create a candidate without merge writes: job=%+v candidates=%+v", job, candidates.items)
	}
}

func TestExtractionService_StartExtraction(t *testing.T) {
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(&fakeMemRepo{items: map[int64]*memory.Memory{}}, extRepo)

	jobID, err := svc.StartExtraction(context.Background(), 100, 1, []int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if jobID <= 0 {
		t.Fatal("expected positive job ID")
	}
	if len(extRepo.created) != 1 {
		t.Fatal("expected 1 job created")
	}
	if extRepo.created[0].ConversationID != 1 {
		t.Fatalf("unexpected conversation: %d", extRepo.created[0].ConversationID)
	}
}

func TestExtractionServiceScheduleDreamUsesTurnAndIdleConfig(t *testing.T) {
	extractions := &fakeExtractionRepo{}
	messages := &fakeDreamMessages{items: []conversation.Message{{ImmutableModel: domain.ImmutableModel{ID: 10, OwnerID: 1}, ConversationID: 2, Content: "hello"}}}
	service := NewExtractionService(&fakeMemRepo{}, extractions, messages)
	service.ConfigureConversations(fakeConversationRepo{projectID: 42})
	cfg := DreamConfig{Enabled: true, TriggerEveryNTurns: 5, IdleTimeout: 3 * time.Minute}
	idleJob, err := service.ScheduleDream(context.Background(), 1, 2, 4, cfg)
	if err != nil || idleJob == nil || idleJob.ProjectID != 42 || idleJob.TriggerReason != "idle" || idleJob.DueAt == nil || !idleJob.DueAt.After(time.Now().UTC()) {
		t.Fatalf("unexpected idle job: job=%+v err=%v", idleJob, err)
	}
	turnJob, err := service.ScheduleDream(context.Background(), 1, 2, 5, cfg)
	if err != nil || turnJob.ID != idleJob.ID || turnJob.TriggerReason != "turns" || turnJob.DueAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("turn trigger did not advance durable job: job=%+v err=%v", turnJob, err)
	}
}

func TestExtractionService_StartExtractionReusesOpenJob(t *testing.T) {
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(&fakeMemRepo{items: map[int64]*memory.Memory{}}, extRepo)

	first, err := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.StartExtraction(context.Background(), 100, 1, []int64{2})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(extRepo.created) != 1 {
		t.Fatalf("expected existing job to be reused, first=%d second=%d created=%d", first, second, len(extRepo.created))
	}
}

func TestExtractionService_CompleteExtraction(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo)
	candidates := &fakeCandidateWriter{}
	svc.ConfigureCandidates(candidates)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1, 2, 3})
	result := &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{
			{Title: "User preference", Content: "User prefers dark mode", Importance: 0.8, Confidence: 0.9},
		},
	}
	err := svc.CompleteExtraction(context.Background(), jobID, 100, result)
	if err != nil {
		t.Fatal(err)
	}

	job, _ := extRepo.FindByID(context.Background(), 100, jobID)
	if job.Status != string(memory.ExtractionCompleted) {
		t.Fatalf("expected completed status, got %s", job.Status)
	}

	items, _ := memRepo.List(context.Background(), 100, nil, nil, 50, 0)
	if len(items) != 0 || len(candidates.items) != 1 {
		t.Fatalf("expected one profile candidate and no direct/summary memory writes, memories=%d candidates=%d", len(items), len(candidates.items))
	}
}

func TestExtractionService_FailExtraction(t *testing.T) {
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(&fakeMemRepo{items: map[int64]*memory.Memory{}}, extRepo)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1, 2, 3})
	svc.FailExtraction(context.Background(), jobID, 100, "extraction failed: timeout")

	job, _ := extRepo.FindByID(context.Background(), 100, jobID)
	if job.Status != string(memory.ExtractionFailed) {
		t.Fatalf("expected failed status, got %s", job.Status)
	}
	if job.ErrorMessage != "extraction failed: timeout" {
		t.Fatalf("unexpected error: %s", job.ErrorMessage)
	}
}

func TestExtractionService_Deduplication(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	memRepo.Create(context.Background(), &memory.Memory{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: 100}}, MemoryType: memory.TypeProfile, Content: "user prefers dark mode for the interface",
	})
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo)
	candidates := &fakeCandidateWriter{}
	svc.ConfigureCandidates(candidates)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	result := &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{
			{Content: "user prefers dark mode for the interface", Confidence: 0.9, Importance: 0.8},
		},
	}
	if err := svc.CompleteExtraction(context.Background(), jobID, 100, result); err != nil {
		t.Fatal(err)
	}
	if err := svc.applyExtractionResults(context.Background(), extRepo.jobs[jobID], result); err != nil {
		t.Fatal(err)
	}

	items, _ := memRepo.List(context.Background(), 100, nil, nil, 50, 0)
	if len(items) != 1 || len(candidates.items) != 1 {
		t.Fatalf("expected idempotent candidate and unchanged active memory, memories=%d candidates=%d", len(items), len(candidates.items))
	}
}

func TestExtractionService_LowConfidenceFiltered(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo)
	candidates := &fakeCandidateWriter{}
	svc.ConfigureCandidates(candidates)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	result := &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{
			{Content: "Speculative preference", Confidence: 0.3, Importance: 0.5},
			{Content: "Confirmed fact", Confidence: 0.9, Importance: 0.7},
		},
	}
	svc.CompleteExtraction(context.Background(), jobID, 100, result)

	if len(candidates.items) != 1 {
		t.Fatalf("expected 1 candidate (low confidence filtered), got %d", len(candidates.items))
	}
	for _, item := range candidates.items {
		if item.Content != "Confirmed fact" {
			t.Fatalf("unexpected content: %s", item.Content)
		}
	}
}
