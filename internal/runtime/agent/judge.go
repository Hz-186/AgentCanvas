package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/strutil"
	"agentcanvas/internal/runtime/evalharness"
)

type LLMJudge struct {
	Client llm.ChatClient
	Model  string
}

type EvalScore struct {
	Score       float64         `json:"score"`
	MaxScore    float64         `json:"max_score"`
	Passed      bool            `json:"passed"`
	Criteria    []EvalCriterion `json:"criteria"`
	Explanation string          `json:"explanation"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

type EvalCriterion struct {
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	Max    float64 `json:"max"`
	Result string  `json:"result"`
}

func (j *LLMJudge) Score(ctx context.Context, provider llm.ChatProviderConfig, task, expected, actual string, expectedTools []string, actualTools []string, temperature *float64) (*EvalScore, error) {
	toolScore := evalharness.Coverage(expectedTools, actualTools)
	contentScore := scoreContent(expected, actual)

	criteria := []EvalCriterion{
		{Name: "tool_usage", Score: toolScore, Max: 1.0, Result: fmt.Sprintf("expected_tools=%v actual_tools=%v", expectedTools, actualTools)},
		{Name: "content_match", Score: contentScore, Max: 1.0, Result: "content similarity evaluated"},
	}

	totalScore := (toolScore + contentScore) / 2.0
	passed := totalScore >= 0.6

	explanation := fmt.Sprintf("Tool score: %.2f, Content score: %.2f, Total: %.2f", toolScore, contentScore, totalScore)

	if j.Client != nil && j.Model != "" {
		llmScore, err := j.scoreWithLLM(ctx, provider, task, expected, actual)
		if err == nil && llmScore != nil {
			criteria = append(criteria, EvalCriterion{
				Name:   "llm_judge",
				Score:  llmScore.Score,
				Max:    llmScore.MaxScore,
				Result: llmScore.Explanation,
			})
			totalScore = (totalScore + llmScore.Score/llmScore.MaxScore) / 2.0
			passed = totalScore >= 0.6
			explanation += " | " + llmScore.Explanation
		}
	}

	return &EvalScore{
		Score:       totalScore,
		MaxScore:    1.0,
		Passed:      passed,
		Criteria:    criteria,
		Explanation: explanation,
	}, nil
}

func (j *LLMJudge) scoreWithLLM(ctx context.Context, provider llm.ChatProviderConfig, task, expected, actual string) (*EvalScore, error) {
	prompt := fmt.Sprintf(`Evaluate how well the agent's output matches the expected output for this task.

Task: %s

Expected Output: %s

Actual Output: %s

Score from 0.0 to 1.0 where:
- 1.0: Perfect match, fully satisfies the task
- 0.7: Mostly correct, minor issues
- 0.4: Partially correct, significant gaps
- 0.0: Completely wrong or irrelevant

Return JSON: {"score": 0.X, "explanation": "brief reason"}`, task, truncate(expected, 1000), truncate(actual, 1000))

	resp, err := j.Client.Chat(ctx, provider, llm.ChatRequest{
		Model:    j.Model,
		Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}},
	})
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(resp.Content)
	content = extractJSONContent(content)
	var result struct {
		Score       float64 `json:"score"`
		Explanation string  `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, err
	}
	return &EvalScore{
		Score:       result.Score,
		MaxScore:    1.0,
		Explanation: result.Explanation,
	}, nil
}

func scoreContent(expected, actual string) float64 {
	if expected == "" {
		return 1.0
	}
	e := strings.ToLower(strings.TrimSpace(expected))
	a := strings.ToLower(strings.TrimSpace(actual))
	if strings.Contains(a, e) {
		return 1.0
	}
	overlap := countWordOverlap(e, a)
	return float64(overlap) / float64(len(strings.Fields(e))+1)
}

func countWordOverlap(a, b string) int {
	aWords := make(map[string]bool)
	for _, w := range strings.Fields(a) {
		if len(w) > 2 {
			aWords[w] = true
		}
	}
	count := 0
	for _, w := range strings.Fields(b) {
		if len(w) > 2 && aWords[w] {
			count++
		}
	}
	return count
}

func truncate(s string, maxLen int) string {
	return strutil.TruncateString(s, maxLen)
}
