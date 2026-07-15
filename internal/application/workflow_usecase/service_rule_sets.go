package workflow_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/workflow"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	"agentcanvas/internal/observability"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/harness/rules"

	"gorm.io/gorm"
)

type CreateRuleSetRequest struct {
	CloneFromRuleSetID int64        `json:"clone_from_rule_set_id"`
	Rules              []rules.Rule `json:"rules"`
}

type UpdateRuleSetRequest struct {
	ExpectedRevision int64        `json:"expected_revision" binding:"required"`
	Rules            []rules.Rule `json:"rules"`
}

type PublishRuleSetRequest struct {
	ExpectedRevision int64 `json:"expected_revision" binding:"required"`
}

type RuleEdgeDecisionRequest struct {
	EdgeID   int64  `json:"edge_id" binding:"required"`
	Decision string `json:"decision" binding:"required"`
}

type ReviewRuleSetRequest struct {
	ExpectedRevision int64                     `json:"expected_revision" binding:"required"`
	Decisions        []RuleEdgeDecisionRequest `json:"decisions" binding:"required"`
}

func (s *Service) CreateRuleSet(ctx context.Context, ownerID, workflowID int64, req CreateRuleSetRequest) (*workflow.RuleSet, error) {
	if s.ruleSets == nil {
		return nil, fmt.Errorf("%w: rule set repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	items := append([]rules.Rule(nil), req.Rules...)
	if req.CloneFromRuleSetID > 0 {
		source, err := s.ruleSets.FindByID(ctx, ownerID, workflowID, req.CloneFromRuleSetID)
		if err != nil {
			return nil, mapNotFound(err)
		}
		items, err = runtimeRulesFromRuleSet(source, false)
		if err != nil {
			return nil, err
		}
	}
	if err := rules.ValidateCustomRules(items); err != nil {
		return nil, fmt.Errorf("%w: custom rules are invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	profile, err := s.GetWorkflowProfile(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	providerID, model := ruleCompilerSelection(profile)
	item := &workflow.RuleSet{
		OwnerID: ownerID, WorkflowID: workflowID,
		CompilerProviderID: providerID, CompilerModel: model,
		TokenEstimatorVersion: rules.DefaultTokenEstimatorVersion,
	}
	nodes, edges, err := draftRows(items)
	if err != nil {
		return nil, err
	}
	if err := s.ruleSets.CreateDraft(ctx, item, nodes, edges); err != nil {
		return nil, err
	}
	return s.ruleSets.FindByID(ctx, ownerID, workflowID, item.ID)
}

func (s *Service) ListRuleSets(ctx context.Context, ownerID, workflowID int64) ([]workflow.RuleSet, error) {
	if s.ruleSets == nil {
		return nil, fmt.Errorf("%w: rule set repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	return s.ruleSets.ListByWorkflow(ctx, ownerID, workflowID)
}

func (s *Service) GetRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64) (*workflow.RuleSet, error) {
	if s.ruleSets == nil {
		return nil, fmt.Errorf("%w: rule set repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.ruleSets.FindByID(ctx, ownerID, workflowID, ruleSetID)
	return item, mapNotFound(err)
}

func (s *Service) LoadActiveRuleSet(ctx context.Context, ownerID, workflowID int64) (*rules.CompiledRuleSet, error) {
	if s.ruleSets == nil || s.profiles == nil {
		return nil, nil
	}
	profile, err := s.profiles.FindByWorkflow(ctx, ownerID, workflowID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	if profile.ActiveRuleSetID == nil || *profile.ActiveRuleSetID <= 0 {
		return nil, nil
	}
	item, err := s.ruleSets.FindByID(ctx, ownerID, workflowID, *profile.ActiveRuleSetID)
	if err != nil {
		return nil, err
	}
	if item.Status != workflow.RuleSetStatusPublished {
		return nil, fmt.Errorf("active rule set %d is not published", item.ID)
	}
	return compiledRuleSetFromRecord(item)
}

func (s *Service) loadPinnedRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64) (*rules.CompiledRuleSet, error) {
	if ruleSetID <= 0 || s.ruleSets == nil {
		return nil, nil
	}
	item, err := s.ruleSets.FindByID(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	if item.Status != workflow.RuleSetStatusPublished && item.Status != workflow.RuleSetStatusSuperseded {
		return nil, fmt.Errorf("pinned rule set %d is not an immutable published version", item.ID)
	}
	return compiledRuleSetFromRecord(item)
}

func compiledRuleSetFromRecord(item *workflow.RuleSet) (*rules.CompiledRuleSet, error) {
	if item == nil {
		return nil, fmt.Errorf("rule set record is nil")
	}
	compiled, err := rules.DecodeCompiledRuleSet(item.CompiledSnapshotJSON)
	if err != nil {
		observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
		return nil, fmt.Errorf("rule set snapshot is invalid: %w", err)
	}
	if compiled.CompiledHash == "" || compiled.CompiledHash != item.CompiledHash {
		observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
		return nil, fmt.Errorf("rule set snapshot hash mismatch")
	}
	if compiled.ID != item.ID || compiled.Version != strconv.Itoa(item.VersionNo) {
		observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
		return nil, fmt.Errorf("rule set snapshot identity mismatch")
	}
	if err := rules.VerifyCompiledHash(compiled); err != nil {
		observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
		return nil, fmt.Errorf("rule set snapshot integrity check failed: %w", err)
	}
	compiled.Prepare()
	return compiled, nil
}

func ruleSetIDValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func (s *Service) UpdateRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64, req UpdateRuleSetRequest) (*workflow.RuleSet, error) {
	if req.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("%w: expected_revision is required", agenterrors.ErrInvalidInput)
	}
	if err := rules.ValidateCustomRules(req.Rules); err != nil {
		return nil, fmt.Errorf("%w: custom rules are invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	item, err := s.GetRuleSet(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	nodes, edges, err := draftRows(req.Rules)
	if err != nil {
		return nil, err
	}
	if err := s.ruleSets.UpdateDraft(ctx, item, nodes, edges, req.ExpectedRevision); err != nil {
		return nil, err
	}
	return s.ruleSets.FindByID(ctx, ownerID, workflowID, ruleSetID)
}

func (s *Service) PublishRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64, idempotencyKey string, req PublishRuleSetRequest) (*workflow.RuleCompileJob, error) {
	if req.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("%w: expected_revision is required", agenterrors.ErrInvalidInput)
	}
	item, err := s.GetRuleSet(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	items, err := runtimeRulesFromRuleSet(item, false)
	if err != nil {
		return nil, err
	}
	compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{RuleSetID: item.ID, Version: strconv.Itoa(item.VersionNo)})
	if err != nil {
		return nil, fmt.Errorf("%w: rule graph is invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	profile, err := s.GetWorkflowProfile(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	if err := preflightMandatoryRules(profile, compiled); err != nil {
		return nil, err
	}
	item.SourceHash = sourceHash(items)
	item.CompilerProviderID, item.CompilerModel = ruleCompilerSelection(profile)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) > 128 {
		return nil, fmt.Errorf("%w: Idempotency-Key exceeds 128 characters", agenterrors.ErrInvalidInput)
	}
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("rule-set:%d:%d:%s", item.ID, item.Revision, item.SourceHash)
	}
	job := &workflow.RuleCompileJob{
		OwnerID: ownerID, WorkflowID: workflowID, RuleSetID: item.ID,
		Revision: item.Revision, SourceHash: item.SourceHash,
		IdempotencyKey:     idempotencyKey,
		CompilerProviderID: item.CompilerProviderID, CompilerModel: item.CompilerModel,
	}
	if err := s.ruleSets.QueueCompilation(ctx, item, job, req.ExpectedRevision); err != nil {
		return nil, err
	}
	if s.ruleCompileQueue != nil {
		_ = s.ruleCompileQueue.Publish(ctx, queueinfra.Job{
			ID: fmt.Sprintf("rule-compile-%d", job.ID), Type: workflow.RuleCompileJobType,
			Payload: map[string]any{"compile_job_id": job.ID},
		})
	}
	return job, nil
}

func (s *Service) GetRuleCompileJob(ctx context.Context, ownerID, workflowID, jobID int64) (*workflow.RuleCompileJob, error) {
	if s.ruleSets == nil {
		return nil, fmt.Errorf("%w: rule set repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.ruleSets.FindCompileJob(ctx, ownerID, workflowID, jobID)
	return item, mapNotFound(err)
}

func (s *Service) ReviewRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID, actorID int64, req ReviewRuleSetRequest) (*workflow.RuleSet, error) {
	decisions := make(map[int64]string, len(req.Decisions))
	for _, decision := range req.Decisions {
		if decision.EdgeID <= 0 || (decision.Decision != workflow.RuleEdgeDecisionAccepted && decision.Decision != workflow.RuleEdgeDecisionRejected) {
			return nil, fmt.Errorf("%w: invalid dependency decision", agenterrors.ErrInvalidInput)
		}
		decisions[decision.EdgeID] = decision.Decision
	}
	item, err := s.ruleSets.UpdateEdgeDecisions(ctx, ownerID, workflowID, ruleSetID, req.ExpectedRevision, decisions)
	if err != nil {
		return nil, err
	}
	return s.compileAndPublishRuleSet(ctx, item, actorID)
}

func (s *Service) RollbackRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID, actorID int64) (*workflow.RuleSet, error) {
	target, err := s.GetRuleSet(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	if target.Status != workflow.RuleSetStatusPublished && target.Status != workflow.RuleSetStatusSuperseded {
		return nil, fmt.Errorf("%w: rollback target is not published", agenterrors.ErrInvalidInput)
	}
	compiled, err := rules.DecodeCompiledRuleSet(target.CompiledSnapshotJSON)
	if err != nil {
		return nil, fmt.Errorf("published rule snapshot is invalid: %w", err)
	}
	if compiled.CompiledHash != target.CompiledHash {
		return nil, fmt.Errorf("published rule snapshot hash mismatch")
	}
	if err := rules.VerifyCompiledHash(compiled); err != nil {
		return nil, fmt.Errorf("published rule snapshot integrity check failed: %w", err)
	}
	compiled.Prepare()
	items := rules.RulesFromCompiled(compiled)
	for index := range items {
		items[index].ManualDependsOn = nil
	}
	edges := acceptedDependencyEdges(target.Edges)
	profile, err := s.GetWorkflowProfile(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	if err := preflightMandatoryRules(profile, compiled); err != nil {
		return nil, err
	}
	clone := &workflow.RuleSet{}
	compiler := func(ruleSetID int64, versionNo int) ([]workflow.RuleNode, []workflow.RuleEdge, []byte, string, string, error) {
		recompiled, compileErr := rules.CompileRuleSet(items, rules.CompileOptions{
			RuleSetID: ruleSetID, Version: strconv.Itoa(versionNo), Edges: edges,
		})
		if compileErr != nil {
			return nil, nil, nil, "", "", compileErr
		}
		nodes, rowsErr := compiledRows(recompiled)
		if rowsErr != nil {
			return nil, nil, nil, "", "", rowsErr
		}
		snapshot, marshalErr := json.Marshal(recompiled)
		if marshalErr != nil {
			return nil, nil, nil, "", "", marshalErr
		}
		return nodes, cloneRuleEdges(target.Edges), snapshot, recompiled.CompiledHash, recompiled.TokenEstimatorVersion, nil
	}
	if err := s.ruleSets.RollbackPublished(ctx, target, clone, actorID, compiler); err != nil {
		return nil, err
	}
	observability.RuleSystemMetrics.RecordRollback()
	return s.ruleSets.FindByID(ctx, ownerID, workflowID, clone.ID)
}

func (s *Service) compileAndPublishRuleSet(ctx context.Context, item *workflow.RuleSet, actorID int64) (*workflow.RuleSet, error) {
	items, err := runtimeRulesFromRuleSet(item, true)
	if err != nil {
		return nil, err
	}
	edges := acceptedDependencyEdges(item.Edges)
	compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{
		RuleSetID: item.ID, Version: strconv.Itoa(item.VersionNo), Edges: edges,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: reviewed rule graph is invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	profile, err := s.GetWorkflowProfile(ctx, item.OwnerID, item.WorkflowID)
	if err != nil {
		return nil, err
	}
	if err := preflightMandatoryRules(profile, compiled); err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(compiled)
	if err != nil {
		return nil, err
	}
	nodes, err := compiledRows(compiled)
	if err != nil {
		return nil, err
	}
	if err := s.ruleSets.PublishCompiled(ctx, item, nodes, item.Edges, snapshot, compiled.CompiledHash, compiled.TokenEstimatorVersion, actorID); err != nil {
		return nil, err
	}
	observability.RuleSystemMetrics.RecordPublished()
	return s.ruleSets.FindByID(ctx, item.OwnerID, item.WorkflowID, item.ID)
}

func ruleCompilerSelection(profile *workflow.Profile) (*int64, string) {
	if profile == nil {
		return nil, ""
	}
	providerID := profile.RuleCompilerProviderID
	if providerID == nil {
		providerID = profile.DefaultProviderID
	}
	model := strings.TrimSpace(profile.RuleCompilerModel)
	if model == "" {
		model = strings.TrimSpace(profile.DefaultModel)
	}
	return providerID, model
}

func draftRows(items []rules.Rule) ([]workflow.RuleNode, []workflow.RuleEdge, error) {
	nodes := make([]workflow.RuleNode, 0, len(items))
	edges := make([]workflow.RuleEdge, 0)
	for _, rule := range items {
		activation, err := json.Marshal(rule.Activation)
		if err != nil {
			return nil, nil, err
		}
		var binding json.RawMessage
		if rule.PolicyBinding != nil {
			binding, err = json.Marshal(rule.PolicyBinding)
			if err != nil {
				return nil, nil, err
			}
		}
		nodes = append(nodes, workflow.RuleNode{
			RuleID: rule.ID, Name: rule.Name, Content: rule.Content,
			Strength: string(rule.Strength), ActivationJSON: activation,
			Priority: rule.Priority, SafetyCritical: rule.SafetyCritical, PolicyBindingJSON: binding,
		})
		for _, dependency := range rule.ManualDependsOn {
			edges = append(edges, workflow.RuleEdge{
				RuleID: rule.ID, DependsOnRuleID: dependency,
				Source: "manual", Decision: workflow.RuleEdgeDecisionAccepted,
			})
		}
	}
	return nodes, edges, nil
}

func runtimeRulesFromRuleSet(item *workflow.RuleSet, includeAcceptedLLM bool) ([]rules.Rule, error) {
	dependencies := map[string][]string{}
	for _, edge := range item.Edges {
		if edge.Decision != workflow.RuleEdgeDecisionAccepted {
			continue
		}
		if edge.Source == "llm" {
			continue
		}
		dependencies[edge.RuleID] = append(dependencies[edge.RuleID], edge.DependsOnRuleID)
	}
	_ = includeAcceptedLLM // LLM edges are supplied separately so provenance remains llm_confirmed.
	items := make([]rules.Rule, 0, len(item.Nodes))
	for _, node := range item.Nodes {
		var activation rules.Activation
		if len(node.ActivationJSON) > 0 {
			if err := json.Unmarshal(node.ActivationJSON, &activation); err != nil {
				return nil, fmt.Errorf("rule %s activation is invalid: %w", node.RuleID, err)
			}
		}
		var binding *rules.PolicyBinding
		if len(node.PolicyBindingJSON) > 0 && string(node.PolicyBindingJSON) != "null" {
			binding = &rules.PolicyBinding{}
			if err := json.Unmarshal(node.PolicyBindingJSON, binding); err != nil {
				return nil, fmt.Errorf("rule %s policy binding is invalid: %w", node.RuleID, err)
			}
		}
		items = append(items, rules.Rule{
			ID: node.RuleID, Name: node.Name, Content: node.Content,
			Strength: rules.RuleStrength(node.Strength), Activation: activation,
			Priority: node.Priority, SafetyCritical: node.SafetyCritical,
			PolicyBinding: binding, ManualDependsOn: append([]string(nil), dependencies[node.RuleID]...),
		})
	}
	return items, nil
}

func acceptedDependencyEdges(items []workflow.RuleEdge) []rules.DependencyEdge {
	out := make([]rules.DependencyEdge, 0, len(items))
	for _, edge := range items {
		if edge.Decision != workflow.RuleEdgeDecisionAccepted {
			continue
		}
		out = append(out, rules.DependencyEdge{
			RuleID: edge.RuleID, DependsOn: edge.DependsOnRuleID,
			Source: edge.Source, Confidence: edge.Confidence, Reason: edge.Reason, Decision: edge.Decision,
		})
	}
	return out
}

func compiledRows(compiled *rules.CompiledRuleSet) ([]workflow.RuleNode, error) {
	nodes := make([]workflow.RuleNode, 0, len(compiled.Rules))
	for _, item := range compiled.Rules {
		activation, err := json.Marshal(item.Rule.Activation)
		if err != nil {
			return nil, err
		}
		var binding json.RawMessage
		if item.Rule.PolicyBinding != nil {
			binding, err = json.Marshal(item.Rule.PolicyBinding)
			if err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, workflow.RuleNode{
			RuleID: item.Rule.ID, Name: item.Rule.Name, Content: item.Rule.Content,
			Strength: string(item.Rule.Strength), ActivationJSON: activation,
			Priority: item.Rule.Priority, SafetyCritical: item.Rule.SafetyCritical,
			PolicyBindingJSON: binding, TokenCost: item.TokenCost,
			TopologicalOrder: item.TopologicalOrder, ContentHash: item.ContentHash,
		})
	}
	return nodes, nil
}

func preflightMandatoryRules(profile *workflow.Profile, compiled *rules.CompiledRuleSet) error {
	if profile == nil || compiled == nil {
		return nil
	}
	var policy struct {
		MaxInputTokens       int `json:"max_input_tokens"`
		ContextWindowTokens  int `json:"context_window_tokens"`
		ReservedOutputTokens int `json:"reserved_output_tokens"`
		SafetyMarginTokens   int `json:"context_safety_margin_tokens"`
	}
	_ = json.Unmarshal(profile.ContextPolicyJSON, &policy)
	budget := policy.MaxInputTokens
	if policy.ContextWindowTokens > 0 {
		windowBudget := policy.ContextWindowTokens - policy.ReservedOutputTokens
		if budget <= 0 || (windowBudget > 0 && windowBudget < budget) {
			budget = windowBudget
		}
	}
	if budget > 0 && compiled.MandatoryTokens+policy.SafetyMarginTokens > budget {
		return fmt.Errorf("%w: mandatory=%d budget=%d", runtimeagent.ErrMandatoryRuleBudgetExceeded, compiled.MandatoryTokens, budget)
	}
	return nil
}

func sourceHash(items []rules.Rule) string {
	cloned := append([]rules.Rule(nil), items...)
	sort.SliceStable(cloned, func(i, j int) bool { return cloned[i].ID < cloned[j].ID })
	data, _ := json.Marshal(cloned)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneRuleEdges(items []workflow.RuleEdge) []workflow.RuleEdge {
	out := make([]workflow.RuleEdge, len(items))
	for index, edge := range items {
		edge.ID = 0
		edge.RuleSetID = 0
		out[index] = edge
	}
	return out
}
