package reflection_usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/reflection"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"

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
	return nil
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
	worker := Worker{Service: Service{Reflections: repo}, Providers: workerProviders{item: providerdomain.ModelProvider{ID: 1, OwnerID: 2,
		Status: providerdomain.StatusActive, ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "https://example.test", EncryptedAPIKey: encrypted, DefaultChatModel: "m"}},
		Secrets: box, LLM: workerChat{response: `{"candidates":[{"kind":"error_lesson","scope":"workflow","trigger_type":"tool_error","root_cause_category":"tool_input","root_cause":"wrong input","corrective_action":"validate input first","lesson":"validate tool input before calling","applicability":"tool calls","evidence_step_indexes":[2],"severity":0.9,"generalizability":0.9,"confidence":0.9,"tags":["tool"]}]}`}}
	job := &reflection.Job{OwnerID: 2, WorkflowID: 3, RunID: 4, NodeID: "n", ProviderID: 1, Model: "m", Mode: "react", Task: "task", PayloadJSON: []byte(`{"stop_reason":"max_iterations_exceeded"}`)}
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
	provider := workerProviders{item: providerdomain.ModelProvider{ID: 1, OwnerID: 2, Status: providerdomain.StatusActive,
		ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "https://example.test", EncryptedAPIKey: encrypted, DefaultChatModel: "m"}}
	badJobs := &workerJobRepo{next: &reflection.Job{OwnerID: 2, WorkflowID: 3, RunID: 4, ProviderID: 1, Model: "m", MaxAttempts: 3,
		PayloadJSON: json.RawMessage(`{"stop_reason":"max_iterations_exceeded"}`)}}
	badWorker := Worker{Service: Service{Reflections: &fakeRepo{}}, Jobs: badJobs, Providers: provider, Secrets: box, LLM: workerChat{response: `not-json`}}
	processed, processErr := badWorker.ProcessNext(context.Background(), "worker")
	if !processed || processErr == nil || !badJobs.failed || badJobs.retryAt == nil || badJobs.completed {
		t.Fatalf("malformed analysis should be retried: processed=%v err=%v jobs=%+v", processed, processErr, badJobs)
	}

	goodJobs := &workerJobRepo{next: &reflection.Job{OwnerID: 2, WorkflowID: 3, RunID: 5, ProviderID: 1, Model: "m", MaxAttempts: 3,
		PayloadJSON: json.RawMessage(`{"stop_reason":"max_iterations_exceeded"}`)}}
	goodWorker := Worker{Service: Service{Reflections: &fakeRepo{}}, Jobs: goodJobs, Providers: provider, Secrets: box,
		LLM: workerChat{response: `{"candidates":[]}`}}
	processed, processErr = goodWorker.ProcessNext(context.Background(), "worker")
	if !processed || processErr != nil || !goodJobs.completed || goodJobs.failed {
		t.Fatalf("valid empty analysis should complete: processed=%v err=%v jobs=%+v", processed, processErr, goodJobs)
	}
}
