package evalharness

import "math"

type RAGCase struct {
	Query             string   `json:"query"`
	ExpectedDocIDs    []string `json:"expected_doc_ids,omitempty"`
	RequiredCitations []string `json:"required_citations,omitempty"`
}

type RetrievalHit struct {
	DocID string  `json:"doc_id"`
	Score float64 `json:"score"`
}

type RAGMetrics struct {
	HitRate       float64 `json:"hit_rate"`
	MRR           float64 `json:"mrr"`
	NDCG          float64 `json:"ndcg"`
	CitationRate  float64 `json:"citation_rate"`
	CandidateSize int     `json:"candidate_size"`
}

type AgentCase struct {
	Task            string   `json:"task"`
	RequiredTools   []string `json:"required_tools,omitempty"`
	ExpectedSchema  string   `json:"expected_schema,omitempty"`
	RiskExpectation string   `json:"risk_expectation,omitempty"`
}

type AgentTrace struct {
	UsedTools        []string `json:"used_tools,omitempty"`
	SchemaCompliant  bool     `json:"schema_compliant"`
	ApprovalRequired bool     `json:"approval_required"`
	TokenSaved       int      `json:"token_saved,omitempty"`
	LatencyMS        int      `json:"latency_ms,omitempty"`
}

type AgentMetrics struct {
	ToolCallAccuracy  float64 `json:"tool_call_accuracy"`
	SchemaCompliance  float64 `json:"schema_compliance"`
	ApprovalPrecision float64 `json:"approval_precision"`
	TokenSaved        int     `json:"token_saved"`
	LatencyMS         int     `json:"latency_ms"`
}

func ScoreRAG(tc RAGCase, hits []RetrievalHit, citations []string) RAGMetrics {
	expected := set(tc.ExpectedDocIDs)
	requiredCitations := set(tc.RequiredCitations)
	metrics := RAGMetrics{CandidateSize: len(hits)}
	if len(expected) > 0 && len(hits) > 0 {
		found := 0
		firstRank := 0
		dcg := 0.0
		idealHits := len(expected)
		if idealHits > len(hits) {
			idealHits = len(hits)
		}
		for i, hit := range hits {
			if expected[hit.DocID] {
				found++
				if firstRank == 0 {
					firstRank = i + 1
				}
				dcg += 1 / log2(float64(i+2))
			}
		}
		metrics.HitRate = float64(found) / float64(len(expected))
		if firstRank > 0 {
			metrics.MRR = 1 / float64(firstRank)
		}
		idcg := 0.0
		for i := 0; i < idealHits; i++ {
			idcg += 1 / log2(float64(i+2))
		}
		if idcg > 0 {
			metrics.NDCG = dcg / idcg
		}
	}
	if len(requiredCitations) > 0 {
		matched := 0
		actual := set(citations)
		for citation := range requiredCitations {
			if actual[citation] {
				matched++
			}
		}
		metrics.CitationRate = float64(matched) / float64(len(requiredCitations))
	}
	return metrics
}

func ScoreAgent(tc AgentCase, trace AgentTrace) AgentMetrics {
	metrics := AgentMetrics{TokenSaved: trace.TokenSaved, LatencyMS: trace.LatencyMS}
	metrics.ToolCallAccuracy = coverage(tc.RequiredTools, trace.UsedTools)
	if trace.SchemaCompliant {
		metrics.SchemaCompliance = 1
	}
	if tc.RiskExpectation == "approval_required" {
		if trace.ApprovalRequired {
			metrics.ApprovalPrecision = 1
		}
	} else {
		metrics.ApprovalPrecision = 1
	}
	return metrics
}

func coverage(required, actual []string) float64 {
	if len(required) == 0 {
		return 1
	}
	actualSet := set(actual)
	matched := 0
	for _, item := range required {
		if actualSet[item] {
			matched++
		}
	}
	return float64(matched) / float64(len(required))
}

func set(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func log2(x float64) float64 {
	return math.Log2(x)
}
