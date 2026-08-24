package reflection_usecase

import (
	"agentcanvas/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentcanvas/internal/domain/reflection"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type fakeRepo struct {
	items      []reflection.Reflection
	marked     []int64
	created    *reflection.Reflection
	usefulness []string
	status     string
}

type fakeRecallLogs struct {
	items   []reflection.RecallLog
	outcome string
	verdict string
}

type fakeReflectionJobs struct{ items []*reflection.Job }

func (f *fakeReflectionJobs) Create(_ context.Context, item *reflection.Job) error {
	for _, existing := range f.items {
		if existing.RunID == item.RunID && existing.TriggerHash == item.TriggerHash {
			return nil
		}
	}
	clone := *item
	clone.PayloadJSON = append(json.RawMessage(nil), item.PayloadJSON...)
	f.items = append(f.items, &clone)
	return nil
}

func (f *fakeReflectionJobs) FindLatestByRun(_ context.Context, ownerID, runID int64) (*reflection.Job, error) {
	for index := len(f.items) - 1; index >= 0; index-- {
		if f.items[index].OwnerID == ownerID && f.items[index].RunID == runID {
			clone := *f.items[index]
			return &clone, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeReflectionJobs) ClaimNext(context.Context, string) (*reflection.Job, error) {
	return nil, errors.New("not found")
}
func (f *fakeReflectionJobs) Complete(context.Context, *reflection.Job) error { return nil }
func (f *fakeReflectionJobs) Fail(context.Context, *reflection.Job, error, *time.Time) error {
	return nil
}

func (f *fakeRecallLogs) Create(_ context.Context, item *reflection.RecallLog) error {
	f.items = append(f.items, *item)
	return nil
}
func (f *fakeRecallLogs) ListByRun(context.Context, int64, int64) ([]reflection.RecallLog, error) {
	return f.items, nil
}

func (f *fakeRecallLogs) ResolveRun(_ context.Context, _ int64, _ int64, outcome string) error {
	f.outcome = outcome
	return nil
}
func (f *fakeRecallLogs) SetVerdict(_ context.Context, _ int64, _ int64, _ int64, verdict string, _ string) error {
	f.verdict = verdict
	return nil
}

func (f *fakeRepo) Create(_ context.Context, item *reflection.Reflection) error {
	item.ID = 99
	f.created = item
	f.items = append(f.items, *item)
	return nil
}
func (f *fakeRepo) Update(context.Context, *reflection.Reflection) error { return nil }
func (f *fakeRepo) FindByID(_ context.Context, ownerID, id int64) (*reflection.Reflection, error) {
	for index := range f.items {
		if f.items[index].OwnerID == ownerID && f.items[index].ID == id {
			clone := f.items[index]
			return &clone, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeRepo) FindActiveByHash(_ context.Context, ownerID, agentID int64, hash string) (*reflection.Reflection, error) {
	for i := range f.items {
		if f.items[i].OwnerID == ownerID && f.items[i].AgentID == agentID && f.items[i].ContentHash == hash {
			return &f.items[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeRepo) ListCandidates(context.Context, reflection.CandidateQuery) ([]reflection.Reflection, error) {
	return f.items, nil
}
func (f *fakeRepo) ListByAgent(_ context.Context, ownerID, agentID int64, status string, _, _ int) ([]reflection.Reflection, error) {
	items := make([]reflection.Reflection, 0)
	for _, item := range f.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && (status == "" || item.Status == status) {
			items = append(items, item)
		}
	}
	return items, nil
}
func (f *fakeRepo) MarkRecalled(_ context.Context, _ int64, ids []int64) error {
	f.marked = append(f.marked, ids...)
	return nil
}
func (f *fakeRepo) UpdateUsefulness(_ context.Context, _ int64, _ int64, verdict string) error {
	f.usefulness = append(f.usefulness, verdict)
	return nil
}
func (f *fakeRepo) SetStatus(_ context.Context, _ int64, _ int64, status string) error {
	f.status = status
	return nil
}

func TestRecallRanksRelevantNodeLessonAndRespectsBudget(t *testing.T) {
	repo := &fakeRepo{items: []reflection.Reflection{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 1}}, AgentID: 2, Mode: "react", Lesson: "分页接口必须继续读取 next page", CorrectiveAction: "循环读取 next_page", Applicability: "分页 API", Importance: .9, Confidence: .9},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, AgentID: 2, Lesson: "写文件前检查目录", CorrectiveAction: "检查目录", Applicability: "文件任务", Importance: .9, Confidence: .9},
	}}
	result, err := (Service{Reflections: repo}).Recall(context.Background(), reflection.RecallRequest{OwnerID: 1, AgentID: 2, RunID: 3, Mode: "react", Task: "读取分页 API 的全部结果", Policy: reflection.DefaultPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lessons) == 0 || result.Lessons[0].ID != 1 || len(repo.marked) == 0 {
		t.Fatalf("%+v", result)
	}
	if len(result.Lessons) != 1 {
		t.Fatalf("irrelevant lessons must not be injected: %+v", result.Lessons)
	}
	if result.Tokens > reflection.DefaultPolicy().RecallTokenBudget {
		t.Fatalf("tokens=%d", result.Tokens)
	}
}

func TestStoreDeduplicatesByContentHash(t *testing.T) {
	repo := &fakeRepo{}
	svc := Service{Reflections: repo}
	first := &reflection.Reflection{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: 1}}, AgentID: 2, Kind: reflection.KindErrorLesson, Lesson: "avoid retry", CorrectiveAction: "change input", Importance: .8, Confidence: .9}
	stored, err := svc.Store(context.Background(), first)
	if err != nil || stored.ID != 99 {
		t.Fatalf("%+v %v", stored, err)
	}
	second := &reflection.Reflection{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: 1}}, AgentID: 2, Kind: reflection.KindErrorLesson, Lesson: "avoid retry", CorrectiveAction: "change input", Importance: .9, Confidence: .95}
	stored, err = svc.Store(context.Background(), second)
	if err != nil || len(repo.items) != 1 || stored.Importance != .9 {
		t.Fatalf("%+v %v items=%d", stored, err, len(repo.items))
	}
}

func TestStoreActivatesHighConfidenceImportantStrategy(t *testing.T) {
	repo := &fakeRepo{}
	stored, err := (Service{Reflections: repo}).Store(context.Background(), &reflection.Reflection{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: 1}}, AgentID: 2,
		Kind: reflection.KindImportantStrategy, Lesson: "reuse the verified pagination strategy", CorrectiveAction: "iterate until next is empty",
		Applicability: "paginated APIs", Importance: .86, Confidence: .9})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != reflection.StatusActive {
		t.Fatalf("strong externally evidenced strategy should become recallable, got %+v", stored)
	}
}

func TestEnqueueUsesDeterministicTriggerHash(t *testing.T) {
	jobs := &fakeReflectionJobs{}
	service := Service{Jobs: jobs}
	for range 2 {
		if err := service.Enqueue(context.Background(), &reflection.Job{BaseModel: domain.BaseModel{OwnerID: 1}, AgentID: 2, RunID: 3, PayloadJSON: json.RawMessage(`{"result":"done"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(jobs.items) != 1 || jobs.items[0].TriggerHash == "" {
		t.Fatalf("terminal reflection job must be idempotent: %+v", jobs.items)
	}
}

func TestRecordEvaluationPassMarksRecalledLessonsHelpful(t *testing.T) {
	repo := &fakeRepo{items: []reflection.Reflection{{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}}, AgentID: 2}}}
	logs := &fakeRecallLogs{items: []reflection.RecallLog{{BaseModel: domain.BaseModel{OwnerID: 1}, ReflectionID: 7, RunID: 3}}}
	Service{Reflections: repo, RecallLogs: logs}.RecordEvaluation(context.Background(), 1, 3, true, "matched expected output")
	if logs.outcome != "eval_passed" || logs.verdict != "helpful" || len(repo.usefulness) != 1 || repo.usefulness[0] != "helpful" {
		t.Fatalf("eval pass was not propagated: logs=%+v usefulness=%+v", logs, repo.usefulness)
	}
}

func TestRecordEvaluationFailureEnqueuesEvidenceBackedFollowUp(t *testing.T) {
	jobs := &fakeReflectionJobs{items: []*reflection.Job{{BaseModel: domain.BaseModel{OwnerID: 1}, AgentID: 2, RunID: 3, ProviderID: 4,
		Model: "model", Mode: "react", Task: "task", PayloadJSON: json.RawMessage(`{"stop_reason":"succeeded"}`), TriggerHash: "terminal", MaxAttempts: 3}}}
	service := Service{Jobs: jobs}
	service.RecordEvaluation(context.Background(), 1, 3, false, "missing required citation")
	service.RecordEvaluation(context.Background(), 1, 3, false, "missing required citation")
	if len(jobs.items) != 2 {
		t.Fatalf("evaluation follow-up must be idempotent, jobs=%d", len(jobs.items))
	}
	var payload map[string]any
	if err := json.Unmarshal(jobs.items[1].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	evaluation, ok := payload["external_evaluation"].(map[string]any)
	if !ok || evaluation["reason"] != "missing required citation" || jobs.items[1].ProviderID != 4 {
		t.Fatalf("external evaluation evidence or provider metadata missing: job=%+v payload=%+v", jobs.items[1], payload)
	}
}

func TestReflectionManagementEnforcesOwnerAndAgentScope(t *testing.T) {
	repo := &fakeRepo{items: []reflection.Reflection{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 1, OwnerID: 10}}, AgentID: 20, Status: reflection.StatusActive},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 10}}, AgentID: 21, Status: reflection.StatusActive},
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 3, OwnerID: 11}}, AgentID: 20, Status: reflection.StatusActive},
	}}
	service := Service{Reflections: repo}
	items, err := service.List(context.Background(), 10, 20, reflection.StatusActive, 50, 0)
	if err != nil || len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("list leaked another owner or agent: items=%+v err=%v", items, err)
	}
	if err := service.SetStatus(context.Background(), 10, 21, 1, UpdateStatusRequest{Status: reflection.StatusArchived}); err == nil {
		t.Fatal("cross-agent status update must be rejected")
	}
	if err := service.SetStatus(context.Background(), 11, 20, 1, UpdateStatusRequest{Status: reflection.StatusArchived}); err == nil {
		t.Fatal("cross-owner status update must be rejected")
	}
}

func TestFeedbackRequiresReflectionToHaveBeenRecalledInRun(t *testing.T) {
	repo := &fakeRepo{items: []reflection.Reflection{{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}}, AgentID: 2, Status: reflection.StatusActive}}}
	service := Service{Reflections: repo, RecallLogs: &fakeRecallLogs{}}
	err := service.Feedback(context.Background(), 1, 3, 7, FeedbackRequest{Verdict: "helpful"})
	if !errors.Is(err, agenterrors.ErrForbidden) || len(repo.usefulness) != 0 {
		t.Fatalf("unrecalled reflection feedback must be rejected: err=%v usefulness=%+v", err, repo.usefulness)
	}
}

func TestHarmfulFeedbackEnqueuesUserCorrectionReflection(t *testing.T) {
	repo := &fakeRepo{items: []reflection.Reflection{{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 7, OwnerID: 1}}, AgentID: 2, Status: reflection.StatusActive}}}
	logs := &fakeRecallLogs{items: []reflection.RecallLog{{BaseModel: domain.BaseModel{OwnerID: 1}, ReflectionID: 7, RunID: 3}}}
	jobs := &fakeReflectionJobs{items: []*reflection.Job{{BaseModel: domain.BaseModel{OwnerID: 1}, AgentID: 2, RunID: 3, ProviderID: 4,
		Model: "model", Mode: "react", Task: "task", PayloadJSON: json.RawMessage(`{"stop_reason":"final_answer"}`), TriggerHash: "terminal"}}}
	service := Service{Reflections: repo, RecallLogs: logs, Jobs: jobs}
	if err := service.Feedback(context.Background(), 1, 3, 7, FeedbackRequest{Verdict: "harmful", Note: "this caused a repeated write"}); err != nil {
		t.Fatal(err)
	}
	if len(jobs.items) != 2 || repo.usefulness[0] != "harmful" {
		t.Fatalf("harmful feedback did not create correction job: jobs=%+v usefulness=%+v", jobs.items, repo.usefulness)
	}
	var payload map[string]any
	if err := json.Unmarshal(jobs.items[1].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	feedback, ok := payload["user_feedback"].(map[string]any)
	if !ok || feedback["verdict"] != "harmful" || feedback["reflection_id"] != float64(7) {
		t.Fatalf("user feedback evidence missing: %+v", payload)
	}
}
