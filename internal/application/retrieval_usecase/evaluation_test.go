package retrieval_usecase

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

func TestEvaluateRankingCalculatesRecallMRRAndNDCG(t *testing.T) {
	metrics := EvaluateRanking(map[string]bool{"a": true, "c": true}, []string{"x", "a", "b", "c"}, 4)
	if metrics.RecallAtK != 1 || metrics.MRR != .5 || metrics.NDCG <= 0 || metrics.NDCG >= 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestOfflineQueryPlanningEvaluationSet(t *testing.T) {
	raw, err := os.ReadFile("testdata/query_planner_eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Query                 string                `json:"query"`
		Turns                 []retrieval.QueryTurn `json:"turns"`
		MustPreserve          []string              `json:"must_preserve"`
		ClarificationRequired bool                  `json:"clarification_required"`
		MinimumSubqueries     int                   `json:"minimum_subqueries"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		plan := BuildQueryPlan(test.Query, test.Turns)
		combined := strings.Join(sortedPlanQueries(plan), " ")
		for _, constraint := range test.MustPreserve {
			if !strings.Contains(strings.ToLower(combined), strings.ToLower(constraint)) {
				t.Errorf("query %q lost hard constraint %q: %+v", test.Query, constraint, plan)
			}
		}
		if plan.NeedsClarification != test.ClarificationRequired {
			t.Errorf("query %q clarification=%v want=%v", test.Query, plan.NeedsClarification, test.ClarificationRequired)
		}
		if len(plan.Subqueries) < test.MinimumSubqueries {
			t.Errorf("query %q subqueries=%d want>=%d", test.Query, len(plan.Subqueries), test.MinimumSubqueries)
		}
	}
}
