package workflow_usecase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/runtime/harness/rules"
)

func TestLoadActiveRuleSetRecomputesSnapshotHash(t *testing.T) {
	compiled, err := rules.CompileRuleSet([]rules.Rule{{
		ID: "tenant.audit", Content: "retain audit logs", Strength: rules.RuleMandatory,
	}}, rules.CompileOptions{RuleSetID: 12, Version: "4"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(compiled)
	if err != nil {
		t.Fatal(err)
	}
	activeID := int64(12)
	profiles := &fakeProfileRepo{items: map[int64]*workflow.Profile{
		20: {ID: 1, OwnerID: 1, WorkflowID: 20, ActiveRuleSetID: &activeID},
	}}
	repository := &activeRuleSetRepo{item: &workflow.RuleSet{
		ID: activeID, OwnerID: 1, WorkflowID: 20, VersionNo: 4, Status: workflow.RuleSetStatusPublished,
		CompiledHash: compiled.CompiledHash, CompiledSnapshotJSON: snapshot,
	}}
	service := &Service{profiles: profiles, ruleSets: repository}
	if _, err := service.LoadActiveRuleSet(context.Background(), 1, 20); err != nil {
		t.Fatalf("valid snapshot must load: %v", err)
	}

	compiled.Rules[0].Rule.Content = "tampered content"
	repository.item.CompiledSnapshotJSON, err = json.Marshal(compiled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.LoadActiveRuleSet(context.Background(), 1, 20)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected content tampering to fail integrity verification, got %v", err)
	}
}

func TestRollbackRuleSetRecompilesSnapshotWithNewIdentity(t *testing.T) {
	original, err := rules.CompileRuleSet([]rules.Rule{
		{ID: "tenant.base", Content: "base", Strength: rules.RuleOptional, Activation: rules.Activation{Always: true}},
		{ID: "tenant.report", Content: "report", Strength: rules.RuleOptional, Activation: rules.Activation{Always: true}},
	}, rules.CompileOptions{RuleSetID: 12, Version: "4"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	repository := &rollbackRuleSetRepo{target: &workflow.RuleSet{
		ID: 12, OwnerID: 1, WorkflowID: 20, VersionNo: 4, Revision: 1,
		Status: workflow.RuleSetStatusSuperseded, SourceHash: "source",
		CompiledHash: original.CompiledHash, CompiledSnapshotJSON: snapshot,
	}}
	service := &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1}}},
		profiles:  &fakeProfileRepo{items: map[int64]*workflow.Profile{20: {ID: 1, OwnerID: 1, WorkflowID: 20}}},
		ruleSets:  repository,
	}
	rolledBack, err := service.RollbackRuleSet(context.Background(), 1, 20, 12, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ID != 99 || rolledBack.VersionNo != 5 {
		t.Fatalf("expected new monotonic rollback identity, got %+v", rolledBack)
	}
	recompiled, err := rules.DecodeCompiledRuleSet(rolledBack.CompiledSnapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	if recompiled.ID != 99 || recompiled.Version != "5" || recompiled.SchemaVersion != 3 {
		t.Fatalf("rollback graph-free snapshot identity was not rebuilt: %+v", recompiled)
	}
	if err := rules.VerifyCompiledHash(recompiled); err != nil {
		t.Fatalf("recompiled rollback hash must verify: %v", err)
	}
}

type activeRuleSetRepo struct {
	workflow.RuleSetRepository
	item *workflow.RuleSet
}

type rollbackRuleSetRepo struct {
	workflow.RuleSetRepository
	target *workflow.RuleSet
	clone  *workflow.RuleSet
}

func (r *rollbackRuleSetRepo) FindByID(_ context.Context, _, _, id int64) (*workflow.RuleSet, error) {
	if r.clone != nil && id == r.clone.ID {
		clone := *r.clone
		return &clone, nil
	}
	clone := *r.target
	clone.CompiledSnapshotJSON = append(json.RawMessage(nil), r.target.CompiledSnapshotJSON...)
	return &clone, nil
}

func (r *rollbackRuleSetRepo) RollbackPublished(_ context.Context, _ *workflow.RuleSet, clone *workflow.RuleSet, publishedBy int64, compile workflow.RuleSetRollbackCompiler) error {
	nodes, snapshot, hash, estimator, err := compile(99, 5)
	if err != nil {
		return err
	}
	nowClone := *r.target
	nowClone.ID = 99
	nowClone.VersionNo = 5
	nowClone.Status = workflow.RuleSetStatusPublished
	nowClone.CompiledSnapshotJSON = snapshot
	nowClone.CompiledHash = hash
	nowClone.TokenEstimatorVersion = estimator
	nowClone.Nodes = nodes
	nowClone.PublishedBy = &publishedBy
	r.clone = &nowClone
	*clone = nowClone
	return nil
}

func (r *activeRuleSetRepo) FindByID(context.Context, int64, int64, int64) (*workflow.RuleSet, error) {
	clone := *r.item
	clone.CompiledSnapshotJSON = append(json.RawMessage(nil), r.item.CompiledSnapshotJSON...)
	return &clone, nil
}
