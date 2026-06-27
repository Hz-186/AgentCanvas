package toolruntime

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/memory"
)

type fakeMemoryRepo struct {
	items   map[int64]*memory.Memory
	readReq struct {
		ownerID int64
		types   []string
		limit   int
	}
	marked []int64
}

func (r *fakeMemoryRepo) Create(ctx context.Context, item *memory.Memory) error {
	if r.items == nil {
		r.items = map[int64]*memory.Memory{}
	}
	if item.ID == 0 {
		item.ID = int64(len(r.items) + 1)
	}
	clone := *item
	r.items[item.ID] = &clone
	*item = clone
	return nil
}

func (r *fakeMemoryRepo) Update(ctx context.Context, item *memory.Memory) error {
	if r.items == nil {
		r.items = map[int64]*memory.Memory{}
	}
	clone := *item
	r.items[item.ID] = &clone
	return nil
}

func (r *fakeMemoryRepo) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	item := r.items[id]
	clone := *item
	return &clone, nil
}

func (r *fakeMemoryRepo) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	return nil, nil
}

func (r *fakeMemoryRepo) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	r.readReq.ownerID = ownerID
	r.readReq.types = memoryTypes
	r.readReq.limit = limit
	return []memory.Memory{{ID: 7, OwnerID: ownerID, MemoryType: memory.TypeSummary, Content: "remember this"}}, nil
}

func (r *fakeMemoryRepo) SoftDelete(ctx context.Context, ownerID, id int64) error { return nil }

func (r *fakeMemoryRepo) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	r.marked = ids
	return nil
}

type fakeMemoryLogRepo struct {
	items []memory.WriteLog
}

func (r *fakeMemoryLogRepo) Create(ctx context.Context, item *memory.WriteLog) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeMemoryLogRepo) ListByRun(ctx context.Context, ownerID, runID int64) ([]memory.WriteLog, error) {
	return r.items, nil
}

func TestMemoryReadToolReadsAndMarksMemory(t *testing.T) {
	repo := &fakeMemoryRepo{}
	tool := MemoryReadTool{Memories: repo}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"memory_types":["summary_memory"],"limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.readReq.ownerID != 1 || repo.readReq.limit != 3 || len(repo.marked) != 1 || repo.marked[0] != 7 {
		t.Fatalf("unexpected repo calls: %+v marked=%v", repo.readReq, repo.marked)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["memory_context"] != "remember this" || output["count"].(float64) != 1 {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestMemoryWriteToolCreatesMemoryAndLog(t *testing.T) {
	repo := &fakeMemoryRepo{}
	logs := &fakeMemoryLogRepo{}
	tool := MemoryWriteTool{Memories: repo, Logs: logs}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2}, json.RawMessage(`{
		"memory_type":"task_memory",
		"title":"Preference",
		"content":"User prefers concise answers",
		"importance":0.8,
		"reason":"User stated preference"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != 1 || repo.items[1].Content != "User prefers concise answers" {
		t.Fatalf("unexpected memory items: %+v", repo.items)
	}
	if len(logs.items) != 1 || logs.items[0].RunID != 2 || logs.items[0].Action != memory.WriteActionCreate {
		t.Fatalf("unexpected logs: %+v", logs.items)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["action"] != memory.WriteActionCreate {
		t.Fatalf("unexpected output: %+v", output)
	}
}
