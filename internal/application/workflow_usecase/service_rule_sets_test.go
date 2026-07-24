package workflow_usecase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/runtime/harness/rules"
)

func TestLoadActiveRuleSetVerifiesSnapshotHash(t *testing.T) {
	set, err := rules.NewRuleSet([]rules.Rule{{ID: "tenant.audit", Content: "retain audit logs", Strength: rules.RuleMandatory}}, 12, "4")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	activeID := int64(12)
	profiles := &fakeProfileRepo{items: map[int64]*workflow.Profile{20: {ID: 1, OwnerID: 1, WorkflowID: 20, ActiveRuleSetID: &activeID}}}
	repository := &activeRuleSetRepo{item: &workflow.RuleSet{ID: activeID, OwnerID: 1, WorkflowID: 20, VersionNo: 4, Status: workflow.RuleSetStatusPublished, RuleHash: set.Hash, RuleSnapshotJSON: snapshot}}
	service := &Service{profiles: profiles, ruleSets: repository}
	if _, err := service.LoadActiveRuleSet(context.Background(), 1, 20); err != nil {
		t.Fatalf("valid snapshot must load: %v", err)
	}
	set.Rules[0].Content = "tampered content"
	repository.item.RuleSnapshotJSON, err = json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.LoadActiveRuleSet(context.Background(), 1, 20); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected content tampering to fail integrity verification, got %v", err)
	}
}

func TestRollbackRuleSetBuildsSnapshotWithNewIdentity(t *testing.T) {
	original, err := rules.NewRuleSet([]rules.Rule{
		{ID: "tenant.base", Content: "base", Strength: rules.RuleOptional, Activation: rules.Activation{Always: true}},
		{ID: "tenant.report", Content: "report", Strength: rules.RuleOptional, Activation: rules.Activation{Always: true}},
	}, 12, "4")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	repository := &rollbackRuleSetRepo{target: &workflow.RuleSet{ID: 12, OwnerID: 1, WorkflowID: 20, VersionNo: 4, Revision: 1, Status: workflow.RuleSetStatusSuperseded, RuleHash: original.Hash, RuleSnapshotJSON: snapshot}}
	service := &Service{workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1}}}, profiles: &fakeProfileRepo{items: map[int64]*workflow.Profile{20: {ID: 1, OwnerID: 1, WorkflowID: 20}}}, ruleSets: repository}
	rolledBack, err := service.RollbackRuleSet(context.Background(), 1, 20, 12, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ID != 99 || rolledBack.VersionNo != 5 {
		t.Fatalf("expected new rollback identity, got %+v", rolledBack)
	}
	set, err := rules.DecodeRuleSet(rolledBack.RuleSnapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	if set.ID != 99 || set.Version != "5" {
		t.Fatalf("rollback identity was not rebuilt: %+v", set)
	}
}

type activeRuleSetRepo struct {
	workflow.RuleSetRepository
	item *workflow.RuleSet
}
type rollbackRuleSetRepo struct {
	workflow.RuleSetRepository
	target, clone *workflow.RuleSet
}

func (r *activeRuleSetRepo) FindByID(context.Context, int64, int64, int64) (*workflow.RuleSet, error) {
	clone := *r.item
	clone.RuleSnapshotJSON = append(json.RawMessage(nil), r.item.RuleSnapshotJSON...)
	return &clone, nil
}

func (r *rollbackRuleSetRepo) FindByID(_ context.Context, _, _, id int64) (*workflow.RuleSet, error) {
	if r.clone != nil && id == r.clone.ID {
		clone := *r.clone
		return &clone, nil
	}
	clone := *r.target
	clone.RuleSnapshotJSON = append(json.RawMessage(nil), r.target.RuleSnapshotJSON...)
	return &clone, nil
}

func (r *rollbackRuleSetRepo) RollbackPublished(_ context.Context, _ *workflow.RuleSet, clone *workflow.RuleSet, publishedBy int64, build workflow.RuleSetRollbackBuilder) error {
	nodes, snapshot, hash, err := build(99, 5)
	if err != nil {
		return err
	}
	nowClone := *r.target
	nowClone.ID = 99
	nowClone.VersionNo = 5
	nowClone.Status = workflow.RuleSetStatusPublished
	nowClone.RuleSnapshotJSON = snapshot
	nowClone.RuleHash = hash
	nowClone.Nodes = nodes
	nowClone.PublishedBy = &publishedBy
	r.clone = &nowClone
	*clone = nowClone
	return nil
}
