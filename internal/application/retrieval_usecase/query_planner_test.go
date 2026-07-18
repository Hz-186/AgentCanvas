package retrieval_usecase

import (
	"strings"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

func TestBuildQueryPlanNormalizesAndPreservesHardConstraints(t *testing.T) {
	plan := BuildQueryPlan("  升级以后 Agent Canvas 一直报 401，怎么回事？！ ", nil)
	if plan.NormalizedQuery == "" || !containsFold(plan.PreciseQuery, "AgentCanvas") || !containsFold(plan.PreciseQuery, "401") {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if !hasConstraint(plan.HardConstraints, "product", "AgentCanvas") || !hasConstraint(plan.HardConstraints, "status_code", "401") {
		t.Fatalf("hard constraints were not preserved: %+v", plan.HardConstraints)
	}
}

func TestBuildQueryPlanSplitsCauseAndResolution(t *testing.T) {
	plan := BuildQueryPlan("AgentCanvas 401 的原因是什么，应该怎么排查解决？", nil)
	if len(plan.Subqueries) != 3 {
		t.Fatalf("subqueries = %+v", plan.Subqueries)
	}
	for _, query := range plan.Subqueries {
		if !containsFold(query, "AgentCanvas") || !containsFold(query, "401") {
			t.Fatalf("subquery lost a hard constraint: %q", query)
		}
	}
}

func TestBuildQueryPlanResolvesSingleConversationSubject(t *testing.T) {
	plan := BuildQueryPlan("它还是不行", []retrieval.QueryTurn{{Role: "user", Content: "AgentCanvas 登录服务返回 401"}})
	if plan.NeedsClarification || !containsFold(plan.ResolvedQuery, "AgentCanvas") {
		t.Fatalf("expected resolved plan, got %+v", plan)
	}
}

func TestBuildQueryPlanRequiresClarificationForAmbiguousReference(t *testing.T) {
	plan := BuildQueryPlan("它还是不行", []retrieval.QueryTurn{{Role: "user", Content: "AgentCanvas 和 Redis 都连接失败"}})
	if !plan.NeedsClarification || plan.ClarificationQuestion == "" {
		t.Fatalf("expected clarification, got %+v", plan)
	}
}

func TestApplyRewriteRejectsVariantsThatDropHardConstraints(t *testing.T) {
	plan := BuildQueryPlan("AgentCanvas v2.1 返回 401", nil)
	applyRewrite(&plan, QueryRewriteResult{PreciseQuery: "authentication failure", Paraphrases: []string{"AgentCanvas v2.1 401 authentication failure", "generic auth issue"}, Confidence: .9})
	if plan.PreciseQuery == "authentication failure" || len(plan.Paraphrases) != 1 {
		t.Fatalf("constraint validation failed: %+v", plan)
	}
}

func containsFold(value, fragment string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(fragment))
}

func hasConstraint(items []retrieval.HardConstraint, kind, value string) bool {
	for _, item := range items {
		if item.Kind == kind && strings.EqualFold(item.Value, value) {
			return true
		}
	}
	return false
}
