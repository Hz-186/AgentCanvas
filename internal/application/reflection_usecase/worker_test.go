package reflection_usecase

import (
	"agentcanvas/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/reflection"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/config"

	"gorm.io/gorm"
)

type workerChat struct{ response string }

func (f workerChat) Chat(context.Context, llm.ChatProviderConfig, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: f.response}, nil
}
func (f workerChat) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

type workerProviders struct{ item providerdomain.ModelProvider }

type workerJobRepo struct {
	next      *reflection.Job
	completed bool
	failed    bool
	retryAt   *time.Time
	failErr   error
}

func (f *workerJobRepo) Create(context.Context, *reflection.Job) error { return nil }
func (f *workerJobRepo) FindLatestByRun(context.Context, int64, int64) (*reflection.Job, error) {
	return nil, gorm.ErrRecordNotFound
}
func (f *workerJobRepo) ClaimNext(context.Context, string) (*reflection.Job, error) {
	if f.next == nil {
		return nil, gorm.ErrRecordNotFound
	}
	job := f.next
	f.next = nil
	job.AttemptCount++
	return job, nil
}
func (f *workerJobRepo) Complete(context.Context, *reflection.Job) error {
	f.completed = true
	return nil
}
func (f *workerJobRepo) Fail(_ context.Context, _ *reflection.Job, _ error, retryAt *time.Time) error {
	f.failed, f.retryAt = true, retryAt
	return f.failErr
}

func (f workerProviders) Create(context.Context, *providerdomain.ModelProvider) error { return nil }
func (f workerProviders) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}
func (f workerProviders) FindByID(context.Context, int64, int64) (*providerdomain.ModelProvider, error) {
	item := f.item
	return &item, nil
}
func (f workerProviders) Update(context.Context, *providerdomain.ModelProvider) error { return nil }
func (f workerProviders) SoftDelete(context.Context, int64, int64) error              { return nil }

func TestWorkerStoresQualifiedTerminalReflection(t *testing.T) {
	box, err := cryptoinfra.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _ := box.Encrypt("key")
	repo := &fakeRepo{}
	worker := Worker{Service: Service{Reflections: repo}, Providers: workerProviders{item: providerdomain.ModelProvider{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 2}},
		Enabled:         providerdomain.ProviderEnabled, ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "https://example.test", EncryptedAPIKey: encrypted, DefaultChatModel: "m"}},
		Secrets: box, LLM: workerChat{response: `{"candidates":[{"kind":"error_lesson","scope":"agent","trigger_type":"tool_error","root_cause_category":"tool_input","root_cause":"wrong input","corrective_action":"validate input first","lesson":"validate tool input before calling","applicability":"tool calls","evidence_step_indexes":[2],"severity":0.9,"generalizability":0.9,"confidence":0.9,"tags":["tool"]}]}`}}
	job := &reflection.Job{BaseModel: domain.BaseModel{OwnerID: 2}, AgentID: 3, RunID: 4, ProviderID: 1, Model: "m", Mode: "react", Task: "task", PayloadJSON: []byte(`{"stop_reason":"max_iterations_exceeded"}`)}
	if err := worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.created == nil || repo.created.Kind != reflection.KindErrorLesson || repo.created.Importance < .65 {
		t.Fatalf("%+v", repo.created)
	}
}

func TestTerminalCandidateRequiresEvidenceOnSuccessfulRun(t *testing.T) {
	policy := reflection.DefaultPolicy()
	candidate := terminalCandidate{Kind: reflection.KindImportantStrategy, CorrectiveAction: "reuse the verified query",
		Lesson: "the verified query is reliable", Applicability: "similar searches", Confidence: .9}
	if terminalCandidateAllowed(json.RawMessage(`{"stop_reason":"final_answer"}`), "final_answer", candidate, policy) {
		t.Fatal("ordinary final answer is not sufficient evidence for a persistent strategy")
	}
	if !terminalCandidateAllowed(json.RawMessage(`{"stop_reason":"final_answer","external_evaluation":{"passed":true,"reason":"matched"}}`), "final_answer", candidate, policy) {
		t.Fatal("positive external evaluation should qualify an important strategy")
	}
	errorCandidate := candidate
	errorCandidate.Kind = reflection.KindErrorLesson
	if !terminalCandidateAllowed(json.RawMessage(`{"stop_reason":"final_answer","external_evaluation":{"passed":false,"reason":"missing citation"}}`), "final_answer", errorCandidate, policy) {
		t.Fatal("failed external evaluation should qualify an error lesson")
	}
	if !terminalCandidateAllowed(json.RawMessage(`{"stop_reason":"final_answer","user_feedback":{"verdict":"harmful","note":"repeated write"}}`), "final_answer", errorCandidate, policy) {
		t.Fatal("harmful user feedback should qualify an error lesson")
	}
}

func TestWorkerRetriesMalformedAnalysisAndCompletesQualifiedJob(t *testing.T) {
	box, err := cryptoinfra.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _ := box.Encrypt("key")
	provider := workerProviders{item: providerdomain.ModelProvider{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 2}}, Enabled: providerdomain.ProviderEnabled,
		ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "https://example.test", EncryptedAPIKey: encrypted, DefaultChatModel: "m"}}
	stateErr := errors.New("persist retry state")
	badJobs := &workerJobRepo{next: &reflection.Job{BaseModel: domain.BaseModel{OwnerID: 2}, AgentID: 3, RunID: 4, ProviderID: 1, Model: "m", MaxAttempts: 3,
		PayloadJSON: json.RawMessage(`{"stop_reason":"max_iterations_exceeded"}`)}, failErr: stateErr}
	badWorker := Worker{Service: Service{Reflections: &fakeRepo{}}, Jobs: badJobs, Providers: provider, Secrets: box, LLM: workerChat{response: `not-json`}}
	processed, processErr := badWorker.ProcessNext(context.Background(), "worker")
	if !processed || processErr == nil || !errors.Is(processErr, stateErr) || !badJobs.failed || badJobs.retryAt == nil || badJobs.completed {
		t.Fatalf("malformed analysis should be retried: processed=%v err=%v jobs=%+v", processed, processErr, badJobs)
	}

	goodJobs := &workerJobRepo{next: &reflection.Job{BaseModel: domain.BaseModel{OwnerID: 2}, AgentID: 3, RunID: 5, ProviderID: 1, Model: "m", MaxAttempts: 3,
		PayloadJSON: json.RawMessage(`{"stop_reason":"max_iterations_exceeded"}`)}}
	goodWorker := Worker{Service: Service{Reflections: &fakeRepo{}}, Jobs: goodJobs, Providers: provider, Secrets: box,
		LLM: workerChat{response: `{"candidates":[]}`}}
	processed, processErr = goodWorker.ProcessNext(context.Background(), "worker")
	if !processed || processErr != nil || !goodJobs.completed || goodJobs.failed {
		t.Fatalf("valid empty analysis should complete: processed=%v err=%v jobs=%+v", processed, processErr, goodJobs)
	}
}

type reliableWorkerRepo struct {
	workerJobRepo
	job       *reflection.Job
	state     reflection.ClaimState
	committed bool
	retried   bool
	failedDLQ bool
}

func (r *reliableWorkerRepo) CreateAndDispatch(context.Context, *reflection.Job) (*reflection.Job, error) {
	return r.job, nil
}
func (r *reliableWorkerRepo) ClaimByID(_ context.Context, _ int64, workerID, token string, lease time.Time) (*reflection.Job, reflection.ClaimState, error) {
	job := *r.job
	job.LockedBy, job.LockToken, job.LeaseExpiresAt = workerID, token, &lease
	job.Status = reflection.JobRunning
	job.AttemptCount++
	r.job = &job
	return &job, r.state, nil
}
func (r *reliableWorkerRepo) RenewLease(context.Context, int64, string, time.Time) error { return nil }
func (r *reliableWorkerRepo) CommitResult(context.Context, int64, string, []reflection.Reflection) error {
	r.committed = true
	return nil
}
func (r *reliableWorkerRepo) RetryAndDispatch(context.Context, int64, string, error, time.Time) error {
	r.retried = true
	return nil
}
func (r *reliableWorkerRepo) FailAndDispatchDLQ(context.Context, int64, string, error, string) error {
	r.failedDLQ = true
	return nil
}
func (r *reliableWorkerRepo) ReleaseInterrupted(context.Context, int64, string) error { return nil }
func (r *reliableWorkerRepo) BackfillPendingDispatches(context.Context, int) (int64, error) {
	return 0, nil
}

type workerDelivery struct {
	envelope reflection.Envelope
	acked    bool
	nacked   bool
	term     bool
}

func (d *workerDelivery) Envelope() reflection.Envelope { return d.envelope }
func (d *workerDelivery) ValidationError() error        { return nil }
func (d *workerDelivery) Ack(context.Context) error     { d.acked = true; return nil }
func (d *workerDelivery) Nak(context.Context, time.Duration) error {
	d.nacked = true
	return nil
}
func (d *workerDelivery) InProgress(context.Context) error { return nil }
func (d *workerDelivery) Term(context.Context) error       { d.term = true; return nil }

func TestQueueRuntimeCommitsBeforeAck(t *testing.T) {
	box, err := cryptoinfra.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _ := box.Encrypt("key")
	repo := &reliableWorkerRepo{job: &reflection.Job{BaseModel: domain.BaseModel{ID: 9, OwnerID: 2}, AgentID: 3, RunID: 4, ProviderID: 1, Model: "m", MaxAttempts: 3,
		PayloadJSON: json.RawMessage(`{"stop_reason":"max_iterations_exceeded"}`)}, state: reflection.ClaimAcquired}
	worker := Worker{Jobs: repo, DispatchEnabled: true, Providers: workerProviders{item: providerdomain.ModelProvider{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 2}},
		Enabled:         providerdomain.ProviderEnabled, ProviderType: providerdomain.TypeOpenAICompatible, EncryptedAPIKey: encrypted, DefaultChatModel: "m"}},
		Secrets: box, LLM: workerChat{response: `{"candidates":[]}`}}
	delivery := &workerDelivery{envelope: reflection.Envelope{SchemaVersion: 1, EventID: "event-1", JobID: 9, DispatchSeq: 1}}
	runtime := &QueueRuntime{Worker: worker, Jobs: repo, Config: config.ReflectionQueueConfig{LeaseSeconds: 180, HeartbeatSeconds: 30}}
	runtime.handleDelivery(context.Background(), "worker", delivery)
	if !repo.committed || !delivery.acked || delivery.nacked {
		t.Fatalf("expected commit before ack: repo=%+v delivery=%+v", repo, delivery)
	}
}
