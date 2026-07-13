package reflection_usecase

import (
	"context"
	"errors"
	"testing"

	"agentcanvas/internal/domain/reflection"
)

type fakeRepo struct {
	items   []reflection.Reflection
	marked  []int64
	created *reflection.Reflection
}

func (f *fakeRepo) Create(_ context.Context, item *reflection.Reflection) error {
	item.ID = 99
	f.created = item
	f.items = append(f.items, *item)
	return nil
}
func (f *fakeRepo) Update(context.Context, *reflection.Reflection) error { return nil }
func (f *fakeRepo) FindByID(context.Context, int64, int64) (*reflection.Reflection, error) {
	return nil, errors.New("not found")
}
func (f *fakeRepo) FindActiveByHash(_ context.Context, ownerID, workflowID int64, hash string) (*reflection.Reflection, error) {
	for i := range f.items {
		if f.items[i].OwnerID == ownerID && f.items[i].WorkflowID == workflowID && f.items[i].ContentHash == hash {
			return &f.items[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeRepo) ListCandidates(context.Context, reflection.CandidateQuery) ([]reflection.Reflection, error) {
	return f.items, nil
}
func (f *fakeRepo) ListByWorkflow(context.Context, int64, int64, string, int, int) ([]reflection.Reflection, error) {
	return f.items, nil
}
func (f *fakeRepo) MarkRecalled(_ context.Context, _ int64, ids []int64) error {
	f.marked = append(f.marked, ids...)
	return nil
}
func (f *fakeRepo) UpdateUsefulness(context.Context, int64, int64, string) error { return nil }
func (f *fakeRepo) SetStatus(context.Context, int64, int64, string) error        { return nil }

func TestRecallRanksRelevantNodeLessonAndRespectsBudget(t *testing.T) {
	repo := &fakeRepo{items: []reflection.Reflection{
		{ID: 1, OwnerID: 1, WorkflowID: 2, NodeID: "n", Mode: "react", Lesson: "分页接口必须继续读取 next page", CorrectiveAction: "循环读取 next_page", Applicability: "分页 API", Importance: .9, Confidence: .9},
		{ID: 2, OwnerID: 1, WorkflowID: 2, Lesson: "写文件前检查目录", CorrectiveAction: "检查目录", Applicability: "文件任务", Importance: .9, Confidence: .9},
	}}
	result, err := (Service{Reflections: repo}).Recall(context.Background(), reflection.RecallRequest{OwnerID: 1, WorkflowID: 2, RunID: 3, NodeID: "n", Mode: "react", Task: "读取分页 API 的全部结果", Policy: reflection.DefaultPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lessons) == 0 || result.Lessons[0].ID != 1 || len(repo.marked) == 0 {
		t.Fatalf("%+v", result)
	}
	if result.Tokens > reflection.DefaultPolicy().RecallTokenBudget {
		t.Fatalf("tokens=%d", result.Tokens)
	}
}

func TestStoreDeduplicatesByContentHash(t *testing.T) {
	repo := &fakeRepo{}
	svc := Service{Reflections: repo}
	first := &reflection.Reflection{OwnerID: 1, WorkflowID: 2, Kind: reflection.KindErrorLesson, Lesson: "avoid retry", CorrectiveAction: "change input", Importance: .8, Confidence: .9}
	stored, err := svc.Store(context.Background(), first)
	if err != nil || stored.ID != 99 {
		t.Fatalf("%+v %v", stored, err)
	}
	second := &reflection.Reflection{OwnerID: 1, WorkflowID: 2, Kind: reflection.KindErrorLesson, Lesson: "avoid retry", CorrectiveAction: "change input", Importance: .9, Confidence: .95}
	stored, err = svc.Store(context.Background(), second)
	if err != nil || len(repo.items) != 1 || stored.Importance != .9 {
		t.Fatalf("%+v %v items=%d", stored, err, len(repo.items))
	}
}
