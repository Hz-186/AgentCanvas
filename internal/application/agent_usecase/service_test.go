package agent_usecase

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/flow"

	"gorm.io/gorm"
)

func TestCreateFlowVersionReusesEquivalentLatestVersion(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{
		{ID: 10, OwnerID: 1, AgentID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`), IsDraft: true},
	}}
	service := newFlowVersionTestService(versions)

	// 位置和结构完全相同（仅 key 顺序不同），应复用已有版本
	created, err := service.CreateFlowVersion(context.Background(), 1, 20, CreateFlowVersionRequest{
		DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"},"_ui":{"x":120,"y":170}}}],"edges":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 10 || created.VersionNo != 1 {
		t.Fatalf("expected existing version, got %+v", created)
	}
	if versions.createCalls != 0 || versions.nextCalls != 0 {
		t.Fatalf("expected no new version, createCalls=%d nextCalls=%d", versions.createCalls, versions.nextCalls)
	}
}

func TestCreateFlowVersionCreatesVersionForPositionChange(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{
		{ID: 10, OwnerID: 1, AgentID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`), IsDraft: true},
	}}
	service := newFlowVersionTestService(versions)

	// 逻辑不变，仅节点位置改变，应创建新版本
	created, err := service.CreateFlowVersion(context.Background(), 1, 20, CreateFlowVersionRequest{
		DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"},"_ui":{"x":480,"y":260}}}],"edges":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 10 || created.VersionNo != 2 {
		t.Fatalf("expected new v2 for position change, got %+v", created)
	}
	if versions.createCalls != 1 || versions.nextCalls != 1 {
		t.Fatalf("expected one new version, createCalls=%d nextCalls=%d", versions.createCalls, versions.nextCalls)
	}
}

func TestCreateFlowVersionCreatesVersionForRuntimeChange(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{
		{ID: 10, OwnerID: 1, AgentID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"}}}],"edges":[]}`), IsPublished: true},
	}}
	service := newFlowVersionTestService(versions)

	created, err := service.CreateFlowVersion(context.Background(), 1, 20, CreateFlowVersionRequest{
		DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"question":"string"}}}],"edges":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 10 || created.VersionNo != 2 {
		t.Fatalf("expected new v2, got %+v", created)
	}
	if versions.createCalls != 1 || versions.nextCalls != 1 {
		t.Fatalf("expected one new version, createCalls=%d nextCalls=%d", versions.createCalls, versions.nextCalls)
	}
}

func newFlowVersionTestService(versions *fakeFlowVersionRepo) *Service {
	return &Service{
		agents:    &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "test", Status: agent.StatusActive}}},
		versions:  versions,
		validator: flow.NewValidator(nil),
	}
}

func rawJSON(raw string) json.RawMessage {
	return json.RawMessage(raw)
}

type fakeAgentRepo struct {
	items map[int64]*agent.Agent
}

func (r *fakeAgentRepo) Create(context.Context, *agent.Agent) error { return nil }

func (r *fakeAgentRepo) ListByOwner(context.Context, int64) ([]agent.Agent, error) { return nil, nil }

func (r *fakeAgentRepo) FindByID(_ context.Context, ownerID, id int64) (*agent.Agent, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeAgentRepo) Update(context.Context, *agent.Agent) error { return nil }

func (r *fakeAgentRepo) SoftDelete(context.Context, int64, int64) error { return nil }

type fakeFlowVersionRepo struct {
	items       []*agent.FlowVersion
	createCalls int
	nextCalls   int
}

func (r *fakeFlowVersionRepo) Create(_ context.Context, item *agent.FlowVersion) error {
	r.createCalls++
	clone := *item
	clone.ID = int64(100 + r.createCalls)
	r.items = append(r.items, &clone)
	*item = clone
	return nil
}

func (r *fakeFlowVersionRepo) ListByAgent(_ context.Context, ownerID, agentID int64) ([]agent.FlowVersion, error) {
	items := make([]agent.FlowVersion, 0, len(r.items))
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeFlowVersionRepo) FindByID(_ context.Context, ownerID, id int64) (*agent.FlowVersion, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeFlowVersionRepo) FindCurrentByAgent(_ context.Context, ownerID, agentID int64) (*agent.FlowVersion, error) {
	var current *agent.FlowVersion
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && item.IsPublished && (current == nil || item.VersionNo > current.VersionNo) {
			current = item
		}
	}
	if current == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *current
	return &clone, nil
}

func (r *fakeFlowVersionRepo) FindLatestByAgent(_ context.Context, ownerID, agentID int64) (*agent.FlowVersion, error) {
	var latest *agent.FlowVersion
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && (latest == nil || item.VersionNo > latest.VersionNo) {
			latest = item
		}
	}
	if latest == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *latest
	return &clone, nil
}

func (r *fakeFlowVersionRepo) NextVersionNo(_ context.Context, ownerID, agentID int64) (int, error) {
	r.nextCalls++
	maxVersion := 0
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && item.VersionNo > maxVersion {
			maxVersion = item.VersionNo
		}
	}
	return maxVersion + 1, nil
}

func (r *fakeFlowVersionRepo) Publish(context.Context, int64, int64, int64) error { return nil }
