package rule_compile_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/workflow"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/harness/rules"
)

func TestCompilerRejectsCyclicLLMSuggestionAndKeepsPublishedVersion(t *testing.T) {
	repo, providers, secrets := compilerFixture(t)
	client := &fakeCompilerLLM{arguments: json.RawMessage(`{"edges":[{"rule_id":"tenant.a","depends_on":"tenant.b","confidence":0.9,"reason":"a"},{"rule_id":"tenant.b","depends_on":"tenant.a","confidence":0.9,"reason":"b"}]}`)}
	service := NewService(repo, providers, secrets, client)
	err := service.ProcessByID(context.Background(), 10, "worker")
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	if repo.failed == nil || repo.published {
		t.Fatalf("failed compilation must not publish, failed=%v published=%v", repo.failed, repo.published)
	}
}

func TestCompilerStoresValidSuggestionsForHumanReview(t *testing.T) {
	repo, providers, secrets := compilerFixture(t)
	repo.job.Attempts = 1
	client := &fakeCompilerLLM{arguments: json.RawMessage(`{"edges":[{"rule_id":"tenant.b","depends_on":"tenant.a","confidence":0.93,"reason":"prerequisite"}]}`)}
	service := NewService(repo, providers, secrets, client)
	if err := service.ProcessByID(context.Background(), 10, "worker"); err != nil {
		t.Fatal(err)
	}
	if repo.completedStatus != workflow.RuleSetStatusReviewRequired || len(repo.suggestions) != 1 || repo.published {
		t.Fatalf("expected review-required suggestions, status=%s suggestions=%+v published=%v", repo.completedStatus, repo.suggestions, repo.published)
	}
}

func TestCompilerPublishesAutomaticallyWhenNoDependencyIsSuggested(t *testing.T) {
	repo, providers, secrets := compilerFixture(t)
	client := &fakeCompilerLLM{arguments: json.RawMessage(`{"edges":[]}`)}
	service := NewService(repo, providers, secrets, client)
	if err := service.ProcessByID(context.Background(), 10, "worker"); err != nil {
		t.Fatal(err)
	}
	if repo.completedStatus != workflow.RuleSetStatusReady || !repo.published {
		t.Fatalf("expected ready rule set to publish, status=%s published=%v", repo.completedStatus, repo.published)
	}
}

func TestCompilerDoesNotRequireReviewForExistingManualDependency(t *testing.T) {
	repo, providers, secrets := compilerFixture(t)
	repo.set.Edges = []workflow.RuleEdge{{
		ID: 1, RuleSetID: repo.set.ID, RuleID: "tenant.b", DependsOnRuleID: "tenant.a",
		Source: "manual", Decision: workflow.RuleEdgeDecisionAccepted,
	}}
	client := &fakeCompilerLLM{arguments: json.RawMessage(`{"edges":[{"rule_id":"tenant.b","depends_on":"tenant.a","confidence":0.99,"reason":"already configured"}]}`)}
	service := NewService(repo, providers, secrets, client)
	if err := service.ProcessByID(context.Background(), 10, "worker"); err != nil {
		t.Fatal(err)
	}
	if repo.completedStatus != workflow.RuleSetStatusReady || len(repo.suggestions) != 0 || !repo.published {
		t.Fatalf("existing manual edge must not create pending review, status=%s suggestions=%+v published=%v", repo.completedStatus, repo.suggestions, repo.published)
	}
}

func TestCompilerTreatsPersistedStaleJobAsConsumed(t *testing.T) {
	repo, providers, secrets := compilerFixture(t)
	repo.claimErr = workflow.ErrRuleCompileStale
	processed, err := NewService(repo, providers, secrets, &fakeCompilerLLM{}).ProcessNext(context.Background(), "worker")
	if err != nil || !processed {
		t.Fatalf("persisted stale claim must be consumed without retry, processed=%v err=%v", processed, err)
	}
}

func TestCompilerDoesNotRetryAfterCompletionDetectsStaleRevision(t *testing.T) {
	repo, providers, secrets := compilerFixture(t)
	repo.completeErr = workflow.ErrRuleCompileStale
	client := &fakeCompilerLLM{arguments: json.RawMessage(`{"edges":[]}`)}
	if err := NewService(repo, providers, secrets, client).ProcessByID(context.Background(), 10, "worker"); err != nil {
		t.Fatalf("persisted stale completion must stop cleanly: %v", err)
	}
	if repo.failed != nil || repo.published {
		t.Fatalf("stale job must not be retried or published, failed=%v published=%v", repo.failed, repo.published)
	}
}

func TestParseDependencySuggestionsRejectsTrailingJSONAndUnknownFields(t *testing.T) {
	items := []rules.Rule{{ID: "tenant.a"}, {ID: "tenant.b"}}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"edges":[]} {"edges":[]}`),
		json.RawMessage(`{"edges":[{"rule_id":"tenant.b","depends_on":"tenant.a","confidence":0.9,"reason":"ok","strength":"mandatory"}]}`),
	} {
		if _, err := parseDependencySuggestions(raw, items); err == nil {
			t.Fatalf("expected payload to be rejected: %s", string(raw))
		}
	}
}

func FuzzParseDependencySuggestions(f *testing.F) {
	f.Add([]byte(`{"edges":[]}`))
	f.Add([]byte(`{"edges":[{"rule_id":"tenant.b","depends_on":"tenant.a","confidence":0.9,"reason":"required"}]}`))
	f.Add([]byte(`{"edges":[{"rule_id":"unknown","depends_on":"tenant.a","confidence":2,"reason":"bad"}]}`))
	items := []rules.Rule{{ID: "tenant.a"}, {ID: "tenant.b"}}
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = parseDependencySuggestions(json.RawMessage(raw), items)
	})
}

func compilerFixture(t *testing.T) (*fakeRuleSetRepo, *fakeProviderRepo, *cryptoinfra.SecretBox) {
	t.Helper()
	secrets, err := cryptoinfra.NewSecretBox("rule-compiler-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Encrypt("test-key")
	if err != nil {
		t.Fatal(err)
	}
	providerID := int64(7)
	repo := &fakeRuleSetRepo{
		job: workflow.RuleCompileJob{ID: 10, OwnerID: 1, WorkflowID: 2, RuleSetID: 3, Revision: 1, SourceHash: "source", Attempts: 3, CompilerProviderID: &providerID, CompilerModel: "cheap-model"},
		set: workflow.RuleSet{ID: 3, OwnerID: 1, WorkflowID: 2, VersionNo: 1, Revision: 1, SourceHash: "source", Status: workflow.RuleSetStatusCompiling, Nodes: []workflow.RuleNode{
			{RuleID: "tenant.a", Content: "a", Strength: string(rules.RuleOptional), ActivationJSON: json.RawMessage(`{"always":true}`)},
			{RuleID: "tenant.b", Content: "b", Strength: string(rules.RuleOptional), ActivationJSON: json.RawMessage(`{"always":true}`)},
		}},
	}
	providers := &fakeProviderRepo{provider: &providerdomain.ModelProvider{ID: providerID, OwnerID: 1, Status: providerdomain.StatusActive, ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "http://example.com", DefaultChatModel: "cheap-model", EncryptedAPIKey: encrypted}}
	return repo, providers, secrets
}

type fakeCompilerLLM struct{ arguments json.RawMessage }

func (f *fakeCompilerLLM) ChatWithTools(context.Context, llm.ChatProviderConfig, llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	return &llm.ToolChatResponse{Message: llm.ChatMessage{ToolCalls: []llm.ToolCall{{Name: "submit_rule_graph", Arguments: f.arguments}}}}, nil
}

type fakeProviderRepo struct{ provider *providerdomain.ModelProvider }

func (f *fakeProviderRepo) Create(context.Context, *providerdomain.ModelProvider) error { return nil }
func (f *fakeProviderRepo) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}
func (f *fakeProviderRepo) FindByID(context.Context, int64, int64) (*providerdomain.ModelProvider, error) {
	if f.provider == nil {
		return nil, errors.New("not found")
	}
	clone := *f.provider
	return &clone, nil
}
func (f *fakeProviderRepo) Update(context.Context, *providerdomain.ModelProvider) error { return nil }
func (f *fakeProviderRepo) SoftDelete(context.Context, int64, int64) error              { return nil }

type fakeRuleSetRepo struct {
	job             workflow.RuleCompileJob
	set             workflow.RuleSet
	failed          error
	completedStatus string
	suggestions     []workflow.RuleEdge
	published       bool
	claimErr        error
	completeErr     error
}

func (f *fakeRuleSetRepo) CreateDraft(context.Context, *workflow.RuleSet, []workflow.RuleNode, []workflow.RuleEdge) error {
	return nil
}
func (f *fakeRuleSetRepo) ListByWorkflow(context.Context, int64, int64) ([]workflow.RuleSet, error) {
	return nil, nil
}
func (f *fakeRuleSetRepo) FindByID(context.Context, int64, int64, int64) (*workflow.RuleSet, error) {
	clone := f.set
	clone.Nodes = append([]workflow.RuleNode(nil), f.set.Nodes...)
	clone.Edges = append([]workflow.RuleEdge(nil), f.set.Edges...)
	return &clone, nil
}
func (f *fakeRuleSetRepo) UpdateDraft(context.Context, *workflow.RuleSet, []workflow.RuleNode, []workflow.RuleEdge, int64) error {
	return nil
}
func (f *fakeRuleSetRepo) QueueCompilation(context.Context, *workflow.RuleSet, *workflow.RuleCompileJob, int64) error {
	return nil
}
func (f *fakeRuleSetRepo) FindCompileJob(context.Context, int64, int64, int64) (*workflow.RuleCompileJob, error) {
	clone := f.job
	return &clone, nil
}
func (f *fakeRuleSetRepo) ClaimCompileJob(context.Context, int64, string) (*workflow.RuleCompileJob, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	clone := f.job
	return &clone, nil
}
func (f *fakeRuleSetRepo) ClaimNextCompileJob(context.Context, string) (*workflow.RuleCompileJob, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	clone := f.job
	return &clone, nil
}
func (f *fakeRuleSetRepo) CompleteCompilation(_ context.Context, _ *workflow.RuleCompileJob, _ []workflow.RuleNode, suggestions []workflow.RuleEdge, _ []byte, _, _, nextStatus string) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	f.completedStatus = nextStatus
	f.suggestions = append([]workflow.RuleEdge(nil), suggestions...)
	f.set.Status = nextStatus
	return nil
}
func (f *fakeRuleSetRepo) FailCompilation(_ context.Context, _ *workflow.RuleCompileJob, cause error, _ *time.Time) error {
	f.failed = cause
	return nil
}
func (f *fakeRuleSetRepo) UpdateEdgeDecisions(context.Context, int64, int64, int64, int64, map[int64]string) (*workflow.RuleSet, error) {
	return nil, nil
}
func (f *fakeRuleSetRepo) PublishCompiled(context.Context, *workflow.RuleSet, []workflow.RuleNode, []workflow.RuleEdge, []byte, string, string, int64) error {
	f.published = true
	return nil
}
func (f *fakeRuleSetRepo) RollbackPublished(context.Context, *workflow.RuleSet, *workflow.RuleSet, int64, workflow.RuleSetRollbackCompiler) error {
	return nil
}
