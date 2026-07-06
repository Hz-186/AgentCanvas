package evalharness

import "testing"

func TestScoreRAGComputesHitRateMRRAndCitations(t *testing.T) {
	metrics := ScoreRAG(RAGCase{ExpectedDocIDs: []string{"doc2", "doc3"}, RequiredCitations: []string{"c1", "c2"}}, []RetrievalHit{
		{DocID: "doc1", Score: 0.9},
		{DocID: "doc2", Score: 0.8},
		{DocID: "doc3", Score: 0.7},
	}, []string{"c2"})
	if metrics.HitRate != 1 || metrics.MRR != 0.5 || metrics.CitationRate != 0.5 || metrics.CandidateSize != 3 {
		t.Fatalf("unexpected RAG metrics: %+v", metrics)
	}
	if metrics.NDCG <= 0 || metrics.NDCG > 1 {
		t.Fatalf("expected bounded nDCG, got %+v", metrics)
	}
}

func TestScoreAgentComputesToolSchemaApprovalAndCostMetrics(t *testing.T) {
	metrics := ScoreAgent(AgentCase{RequiredTools: []string{"search", "approval"}, RiskExpectation: "approval_required"}, AgentTrace{
		UsedTools:        []string{"search"},
		SchemaCompliant:  true,
		ApprovalRequired: true,
		TokenSaved:       1200,
		LatencyMS:        80,
	})
	if metrics.ToolCallAccuracy != 0.5 || metrics.SchemaCompliance != 1 || metrics.ApprovalPrecision != 1 {
		t.Fatalf("unexpected agent metrics: %+v", metrics)
	}
	if metrics.TokenSaved != 1200 || metrics.LatencyMS != 80 {
		t.Fatalf("expected cost metrics to be retained, got %+v", metrics)
	}
}
