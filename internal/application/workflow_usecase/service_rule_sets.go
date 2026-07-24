package workflow_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"agentcanvas/internal/domain/workflow"
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
		items, err = rulesFromRuleSet(source)
		if err != nil {
			return nil, err
		}
	}
	normalized, err := rules.ValidateRules(items)
	if err != nil {
		return nil, fmt.Errorf("%w: rules are invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	nodes, err := ruleRows(normalized)
	if err != nil {
		return nil, err
	}
	item := &workflow.RuleSet{OwnerID: ownerID, WorkflowID: workflowID}
	if err := s.ruleSets.CreateDraft(ctx, item, nodes); err != nil {
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

func (s *Service) LoadActiveRuleSet(ctx context.Context, ownerID, workflowID int64) (*rules.RuleSet, error) {
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
	return ruleSetFromRecord(item)
}

func (s *Service) loadPinnedRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64) (*rules.RuleSet, error) {
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
	return ruleSetFromRecord(item)
}

func ruleSetFromRecord(item *workflow.RuleSet) (*rules.RuleSet, error) {
	if item == nil {
		return nil, fmt.Errorf("rule set record is nil")
	}
	set, err := rules.DecodeRuleSet(item.RuleSnapshotJSON)
	if err != nil || set.Hash != item.RuleHash || set.ID != item.ID || set.Version != strconv.Itoa(item.VersionNo) {
		observability.RuleSystemMetrics.RecordSnapshotIntegrityFailure()
		if err != nil {
			return nil, fmt.Errorf("rule set snapshot is invalid: %w", err)
		}
		return nil, fmt.Errorf("rule set snapshot identity mismatch")
	}
	return set, nil
}

func ruleSetIDValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func ruleSetRules(set *rules.RuleSet) []rules.Rule {
	if set == nil {
		return nil
	}
	return append([]rules.Rule(nil), set.Rules...)
}

func (s *Service) UpdateRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID int64, req UpdateRuleSetRequest) (*workflow.RuleSet, error) {
	if req.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("%w: expected_revision is required", agenterrors.ErrInvalidInput)
	}
	normalized, err := rules.ValidateRules(req.Rules)
	if err != nil {
		return nil, fmt.Errorf("%w: rules are invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	item, err := s.GetRuleSet(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	nodes, err := ruleRows(normalized)
	if err != nil {
		return nil, err
	}
	if err := s.ruleSets.UpdateDraft(ctx, item, nodes, req.ExpectedRevision); err != nil {
		return nil, err
	}
	return s.ruleSets.FindByID(ctx, ownerID, workflowID, ruleSetID)
}

func (s *Service) PublishRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID, actorID int64, req PublishRuleSetRequest) (*workflow.RuleSet, error) {
	if req.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("%w: expected_revision is required", agenterrors.ErrInvalidInput)
	}
	item, err := s.GetRuleSet(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	if item.Status == workflow.RuleSetStatusPublished && item.Revision == req.ExpectedRevision {
		return item, nil
	}
	items, err := rulesFromRuleSet(item)
	if err != nil {
		return nil, err
	}
	set, err := rules.NewRuleSet(items, item.ID, strconv.Itoa(item.VersionNo))
	if err != nil {
		return nil, fmt.Errorf("%w: rule set is invalid: %v", agenterrors.ErrInvalidInput, err)
	}
	profile, err := s.GetWorkflowProfile(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	if err := preflightMandatoryRules(profile, set.Rules); err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(set)
	if err != nil {
		return nil, err
	}
	nodes, err := ruleRows(set.Rules)
	if err != nil {
		return nil, err
	}
	if err := s.ruleSets.Publish(ctx, item, nodes, snapshot, set.Hash, actorID, req.ExpectedRevision); err != nil {
		return nil, err
	}
	observability.RuleSystemMetrics.RecordPublished()
	return s.ruleSets.FindByID(ctx, ownerID, workflowID, ruleSetID)
}

func (s *Service) RollbackRuleSet(ctx context.Context, ownerID, workflowID, ruleSetID, actorID int64) (*workflow.RuleSet, error) {
	target, err := s.GetRuleSet(ctx, ownerID, workflowID, ruleSetID)
	if err != nil {
		return nil, err
	}
	if target.Status != workflow.RuleSetStatusPublished && target.Status != workflow.RuleSetStatusSuperseded {
		return nil, fmt.Errorf("%w: rollback target is not published", agenterrors.ErrInvalidInput)
	}
	set, err := rules.DecodeRuleSet(target.RuleSnapshotJSON)
	if err != nil || set.Hash != target.RuleHash {
		return nil, fmt.Errorf("published rule snapshot is invalid")
	}
	profile, err := s.GetWorkflowProfile(ctx, ownerID, workflowID)
	if err != nil {
		return nil, err
	}
	if err := preflightMandatoryRules(profile, set.Rules); err != nil {
		return nil, err
	}
	clone := &workflow.RuleSet{}
	build := func(newRuleSetID int64, versionNo int) ([]workflow.RuleNode, []byte, string, error) {
		cloned, buildErr := rules.NewRuleSet(set.Rules, newRuleSetID, strconv.Itoa(versionNo))
		if buildErr != nil {
			return nil, nil, "", buildErr
		}
		nodes, rowsErr := ruleRows(cloned.Rules)
		if rowsErr != nil {
			return nil, nil, "", rowsErr
		}
		snapshot, marshalErr := json.Marshal(cloned)
		if marshalErr != nil {
			return nil, nil, "", marshalErr
		}
		return nodes, snapshot, cloned.Hash, nil
	}
	if err := s.ruleSets.RollbackPublished(ctx, target, clone, actorID, build); err != nil {
		return nil, err
	}
	observability.RuleSystemMetrics.RecordRollback()
	return s.ruleSets.FindByID(ctx, ownerID, workflowID, clone.ID)
}

func ruleRows(items []rules.Rule) ([]workflow.RuleNode, error) {
	nodes := make([]workflow.RuleNode, 0, len(items))
	for _, rule := range items {
		activation, err := json.Marshal(rule.Activation)
		if err != nil {
			return nil, err
		}
		var binding json.RawMessage
		if rule.PolicyBinding != nil {
			binding, err = json.Marshal(rule.PolicyBinding)
			if err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, workflow.RuleNode{RuleID: rule.ID, Name: rule.Name, Content: rule.Content, Strength: string(rule.Strength), ActivationJSON: activation, Priority: rule.Priority, SafetyCritical: rule.SafetyCritical, PolicyBindingJSON: binding})
	}
	return nodes, nil
}

func rulesFromRuleSet(item *workflow.RuleSet) ([]rules.Rule, error) {
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
		items = append(items, rules.Rule{ID: node.RuleID, Name: node.Name, Content: node.Content, Strength: rules.RuleStrength(node.Strength), Activation: activation, Priority: node.Priority, SafetyCritical: node.SafetyCritical, PolicyBinding: binding})
	}
	return rules.ValidateRules(items)
}

func preflightMandatoryRules(profile *workflow.Profile, custom []rules.Rule) error {
	if profile == nil {
		return nil
	}
	items, err := rules.RuntimeRules(custom, false)
	if err != nil {
		return err
	}
	_, trace := rules.SelectMandatoryRules(items)
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
	if budget > 0 && trace.MandatoryTokens+policy.SafetyMarginTokens > budget {
		return fmt.Errorf("%w: mandatory=%d budget=%d", runtimeagent.ErrMandatoryRuleBudgetExceeded, trace.MandatoryTokens, budget)
	}
	return nil
}
