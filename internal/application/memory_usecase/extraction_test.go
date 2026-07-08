package memory_usecase

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/memory"
)

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

type fakeMergeRepo struct {
	logs []memory.MergeLog
	err  error
}

func (r *fakeMergeRepo) Create(ctx context.Context, log *memory.MergeLog) error {
	if r.err != nil {
		return r.err
	}
	r.logs = append(r.logs, *log)
	return nil
}

func (r *fakeMergeRepo) ListByOwner(ctx context.Context, ownerID int64, limit int) ([]memory.MergeLog, error) {
	return r.logs, nil
}

func TestExtractionService_CompleteExtractionFailsWhenCreateFails(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	memRepo.created = nil
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo, &fakeMergeRepo{})

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	memRepo.createErr = errors.New("create failed")
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

func TestExtractionService_CompleteExtractionFailsWhenMergeLogFails(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	memRepo.Create(context.Background(), &memory.Memory{OwnerID: 100, MemoryType: memory.TypeProfile, Content: "alpha beta gamma delta epsilon zeta eta theta", Importance: 0.1})
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo, &fakeMergeRepo{err: errors.New("merge log failed")})

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	err := svc.CompleteExtraction(context.Background(), jobID, 100, &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{{Content: "alpha beta gamma delta epsilon zeta eta iota", Confidence: 0.9, Importance: 0.9}},
	})
	if err == nil {
		t.Fatal("expected merge log error")
	}
	job, _ := extRepo.FindByID(context.Background(), 100, jobID)
	if job.Status != string(memory.ExtractionFailed) {
		t.Fatalf("expected failed job, got %s", job.Status)
	}
}

var _ memory.MergeLogRepository = (*fakeMergeRepo)(nil)

func TestExtractionService_StartExtraction(t *testing.T) {
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(&fakeMemRepo{items: map[int64]*memory.Memory{}}, extRepo, &fakeMergeRepo{})

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

func TestExtractionService_StartExtractionReusesOpenJob(t *testing.T) {
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(&fakeMemRepo{items: map[int64]*memory.Memory{}}, extRepo, &fakeMergeRepo{})

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
	mergeRepo := &fakeMergeRepo{}
	svc := NewExtractionService(memRepo, extRepo, mergeRepo)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1, 2, 3})
	result := &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{
			{Title: "User preference", Content: "User prefers dark mode", Importance: 0.8, Confidence: 0.9},
		},
		SummaryMemories: []memory.ExtractedMemoryItem{
			{Title: "Key decision", Content: "Chose Gin framework for API", Importance: 0.7, Confidence: 0.85},
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
	if len(items) != 2 {
		t.Fatalf("expected 2 memories created, got %d", len(items))
	}
}

func TestExtractionService_FailExtraction(t *testing.T) {
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(&fakeMemRepo{items: map[int64]*memory.Memory{}}, extRepo, &fakeMergeRepo{})

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
		OwnerID: 100, MemoryType: memory.TypeProfile, Content: "user prefers dark mode for the interface",
	})
	extRepo := &fakeExtractionRepo{}
	mergeRepo := &fakeMergeRepo{}
	svc := NewExtractionService(memRepo, extRepo, mergeRepo)

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	result := &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{
			{Content: "user prefers dark mode for the interface", Confidence: 0.9, Importance: 0.8},
		},
	}
	svc.CompleteExtraction(context.Background(), jobID, 100, result)

	items, _ := memRepo.List(context.Background(), 100, nil, nil, 50, 0)
	if len(items) > 1 {
		t.Fatalf("expected no duplicate memory created, got %d items", len(items))
	}
}

func TestExtractionService_LowConfidenceFiltered(t *testing.T) {
	memRepo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	extRepo := &fakeExtractionRepo{}
	svc := NewExtractionService(memRepo, extRepo, &fakeMergeRepo{})

	jobID, _ := svc.StartExtraction(context.Background(), 100, 1, []int64{1})
	result := &memory.ExtractionResult{
		ProfileMemories: []memory.ExtractedMemoryItem{
			{Content: "Speculative preference", Confidence: 0.3, Importance: 0.5},
			{Content: "Confirmed fact", Confidence: 0.9, Importance: 0.7},
		},
	}
	svc.CompleteExtraction(context.Background(), jobID, 100, result)

	items, _ := memRepo.List(context.Background(), 100, nil, nil, 50, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 memory (low confidence filtered), got %d", len(items))
	}
	if items[0].Content != "Confirmed fact" {
		t.Fatalf("unexpected content: %s", items[0].Content)
	}
}
