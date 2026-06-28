package agent

import (
	"encoding/json"

	domainagent "agentcanvas/internal/domain/workflow"
)

type EvalMetrics struct {
	TotalCases       int     `json:"total_cases"`
	Passed           int     `json:"passed"`
	Failed           int     `json:"failed"`
	PassRate         float64 `json:"pass_rate"`
	AvgLatencyMS     int     `json:"avg_latency_ms"`
	AvgToolCalls     float64 `json:"avg_tool_calls"`
	AvgTokens        int     `json:"avg_tokens"`
	ToolSuccessRate  float64 `json:"tool_success_rate"`
	MaxIterHitRate   float64 `json:"max_iter_hit_rate"`
	JSONCompliance   float64 `json:"json_compliance"`
	ApprovalAccuracy float64 `json:"approval_accuracy"`
}

type evalResultMetrics struct {
	ToolCalls       int  `json:"tool_calls"`
	TotalTokens     int  `json:"total_tokens"`
	ToolSuccess     bool `json:"tool_success"`
	MaxIterExceeded bool `json:"max_iter_exceeded"`
	JSONCompliant   bool `json:"json_compliant"`
	ApprovalCorrect bool `json:"approval_correct"`
}

func AggregateEvalMetrics(results []domainagent.EvalResult) *EvalMetrics {
	if len(results) == 0 {
		return &EvalMetrics{}
	}
	m := &EvalMetrics{TotalCases: len(results)}
	var totalLatency, totalToolCalls, totalTokens int
	var toolSuccess, maxIterHit, jsonCompliant, approvalCorrect int
	for _, r := range results {
		if r.Score >= 0.6 {
			m.Passed++
		} else {
			m.Failed++
		}
		totalLatency += r.LatencyMS
		ext := parseEvalMetrics(r.MetricsJSON)
		totalToolCalls += ext.ToolCalls
		totalTokens += ext.TotalTokens
		if ext.ToolSuccess {
			toolSuccess++
		}
		if ext.MaxIterExceeded {
			maxIterHit++
		}
		if ext.JSONCompliant {
			jsonCompliant++
		}
		if ext.ApprovalCorrect {
			approvalCorrect++
		}
	}
	m.PassRate = float64(m.Passed) / float64(m.TotalCases)
	if m.TotalCases > 0 {
		m.AvgLatencyMS = totalLatency / m.TotalCases
		m.AvgToolCalls = float64(totalToolCalls) / float64(m.TotalCases)
		m.AvgTokens = totalTokens / m.TotalCases
		m.ToolSuccessRate = float64(toolSuccess) / float64(m.TotalCases)
		m.MaxIterHitRate = float64(maxIterHit) / float64(m.TotalCases)
		m.JSONCompliance = float64(jsonCompliant) / float64(m.TotalCases)
		m.ApprovalAccuracy = float64(approvalCorrect) / float64(m.TotalCases)
	}
	return m
}

func parseEvalMetrics(raw json.RawMessage) evalResultMetrics {
	var m evalResultMetrics
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}
