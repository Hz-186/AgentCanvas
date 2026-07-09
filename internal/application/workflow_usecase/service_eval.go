package workflow_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentcanvas/internal/domain/workflow"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func (s *Service) CreateEvalDataset(
	ctx context.Context,
	ownerID, workflowID int64,
	req CreateEvalDatasetRequest,
) (*workflow.EvalDataset, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: eval dataset name is required", agenterrors.ErrInvalidInput)
	}
	item := &workflow.EvalDataset{
		OwnerID:     ownerID,
		WorkflowID:  workflowID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Status:      workflow.EvalDatasetStatusActive,
	}
	if err := s.evals.CreateDataset(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListEvalDatasets(ctx context.Context, ownerID, workflowID int64) ([]workflow.EvalDataset, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	return s.evals.ListDatasetsByWorkflow(ctx, ownerID, workflowID)
}

func (s *Service) CreateEvalCase(
	ctx context.Context,
	ownerID, datasetID int64,
	req CreateEvalCaseRequest,
) (*workflow.EvalCase, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	dataset, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: eval case name is required", agenterrors.ErrInvalidInput)
	}
	inputJSON, err := normalizeRawJSONObject(req.InputJSON, "input_json")
	if err != nil {
		return nil, err
	}
	expectedJSON, err := normalizeOptionalRawJSON(req.ExpectedJSON, "expected_json")
	if err != nil {
		return nil, err
	}
	tagsJSON, err := normalizeOptionalRawJSON(req.TagsJSON, "tags_json")
	if err != nil {
		return nil, err
	}
	requiredToolsJSON, err := normalizeOptionalRawJSON(req.RequiredToolsJSON, "required_tools_json")
	if err != nil {
		return nil, err
	}
	item := &workflow.EvalCase{
		OwnerID:           ownerID,
		DatasetID:         dataset.ID,
		Name:              name,
		InputJSON:         inputJSON,
		ExpectedJSON:      expectedJSON,
		TagsJSON:          tagsJSON,
		RequiredToolsJSON: requiredToolsJSON,
	}
	if err := s.evals.CreateCase(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListEvalCases(
	ctx context.Context,
	ownerID, datasetID int64,
) ([]workflow.EvalCase, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListCasesByDataset(ctx, ownerID, datasetID)
}

func (s *Service) RunEvalDataset(ctx context.Context, ownerID, datasetID int64, req RunEvalDatasetRequest) (*workflow.EvalRun, []workflow.EvalResult, error) {
	if s.evals == nil {
		return nil, nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	dataset, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID)
	if err != nil {
		return nil, nil, mapNotFound(err)
	}
	cases, err := s.evals.ListCasesByDataset(ctx, ownerID, datasetID)
	if err != nil {
		return nil, nil, err
	}
	started := time.Now().UTC()
	evalRun := &workflow.EvalRun{
		OwnerID:       ownerID,
		WorkflowID:    dataset.WorkflowID,
		DatasetID:     dataset.ID,
		FlowVersionID: req.FlowVersionID,
		Status:        workflow.EvalRunStatusRunning,
		TotalCases:    len(cases),
		StartedAt:     started,
	}
	if err := s.evals.CreateEvalRun(ctx, evalRun); err != nil {
		return nil, nil, err
	}
	results := make([]workflow.EvalResult, 0, len(cases))
	for _, evalCase := range cases {
		caseStarted := time.Now().UTC()
		input := map[string]any{}
		if err := json.Unmarshal(evalCase.InputJSON, &input); err != nil {
			result := failedEvalResult(ownerID, evalRun.ID, evalCase.ID, nil, nil, "invalid input_json: "+err.Error(), int(time.Since(caseStarted).Milliseconds()))
			_ = s.evals.CreateEvalResult(ctx, result)
			results = append(results, *result)
			continue
		}
		agentRun, output, runErr := s.RunWorkflow(ctx, ownerID, dataset.WorkflowID, RunWorkflowRequest{FlowVersionID: req.FlowVersionID, Input: input})
		var agentRunID *int64
		if agentRun != nil {
			agentRunID = &agentRun.ID
		}
		outputJSON, _ := json.Marshal(output)
		if runErr != nil {
			result := failedEvalResult(ownerID, evalRun.ID, evalCase.ID, agentRunID, outputJSON, runErr.Error(), int(time.Since(caseStarted).Milliseconds()))
			if err := s.evals.CreateEvalResult(ctx, result); err != nil {
				return evalRun, results, err
			}
			results = append(results, *result)
			continue
		}
		scoreResult := scoreEvalOutputDetailed(output, evalCase.ExpectedJSON, evalCase.RequiredToolsJSON)
		score, reason := scoreResult.Score, scoreResult.Reason
		status := "failed"
		if score >= 1 {
			status = "passed"
		}
		metricsJSON, _ := json.Marshal(scoreResult.Metrics)
		result := &workflow.EvalResult{
			OwnerID:       ownerID,
			EvalRunID:     evalRun.ID,
			EvalCaseID:    evalCase.ID,
			WorkflowRunID: agentRunID,
			Status:        status,
			Score:         score,
			Reason:        reason,
			OutputJSON:    outputJSON,
			MetricsJSON:   metricsJSON,
			LatencyMS:     int(time.Since(caseStarted).Milliseconds()),
			CreatedAt:     time.Now().UTC(),
		}
		if err := s.evals.CreateEvalResult(ctx, result); err != nil {
			return evalRun, results, err
		}
		results = append(results, *result)
	}
	passed := 0
	for _, result := range results {
		if result.Status == "passed" {
			passed++
		}
	}
	finished := time.Now().UTC()
	evalRun.PassedCases = passed
	evalRun.FailedCases = len(results) - passed
	if len(results) > 0 {
		evalRun.SuccessRate = float64(passed) / float64(len(results))
	}
	evalRun.Status = workflow.EvalRunStatusCompleted
	evalRun.FinishedAt = &finished
	evalRun.SummaryJSON, _ = json.Marshal(map[string]any{
		"total_cases":  evalRun.TotalCases,
		"passed_cases": evalRun.PassedCases,
		"failed_cases": evalRun.FailedCases,
		"success_rate": evalRun.SuccessRate,
		"metrics":      summarizeEvalMetrics(results),
	})
	if err := s.evals.UpdateEvalRun(ctx, evalRun); err != nil {
		return evalRun, results, err
	}
	return evalRun, results, nil
}

func (s *Service) ListEvalRuns(ctx context.Context, ownerID, datasetID int64) ([]workflow.EvalRun, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListEvalRunsByDataset(ctx, ownerID, datasetID)
}

func (s *Service) GetEvalTrend(ctx context.Context, ownerID, datasetID int64) (*EvalTrend, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	dataset, err := s.evals.FindDatasetByID(ctx, ownerID, datasetID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	runs, err := s.evals.ListEvalRunsByDataset(ctx, ownerID, datasetID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
	points := make([]EvalTrendPoint, 0, len(runs))
	for _, run := range runs {
		points = append(points, EvalTrendPoint{
			EvalRunID:     run.ID,
			FlowVersionID: run.FlowVersionID,
			Status:        run.Status,
			TotalCases:    run.TotalCases,
			PassedCases:   run.PassedCases,
			FailedCases:   run.FailedCases,
			SuccessRate:   run.SuccessRate,
			Metrics:       evalRunSummaryMetrics(run.SummaryJSON),
			StartedAt:     run.StartedAt,
			FinishedAt:    run.FinishedAt,
		})
	}
	trend := &EvalTrend{DatasetID: dataset.ID, WorkflowID: dataset.WorkflowID, Points: points, Delta: map[string]any{}, TrendSummary: map[string]any{"run_count": len(points)}}
	if len(points) == 0 {
		return trend, nil
	}
	latest := points[len(points)-1]
	best := latest
	for _, point := range points {
		if point.SuccessRate > best.SuccessRate || (point.SuccessRate == best.SuccessRate && point.StartedAt.After(best.StartedAt)) {
			best = point
		}
	}
	trend.Latest = &latest
	trend.Best = &best
	trend.Delta = buildEvalTrendDelta(points[0], latest)
	trend.TrendSummary = map[string]any{
		"run_count":              len(points),
		"latest_eval_run_id":     latest.EvalRunID,
		"latest_flow_version_id": latest.FlowVersionID,
		"latest_success_rate":    latest.SuccessRate,
		"best_eval_run_id":       best.EvalRunID,
		"best_flow_version_id":   best.FlowVersionID,
		"best_success_rate":      best.SuccessRate,
		"success_rate_delta":     latest.SuccessRate - points[0].SuccessRate,
	}
	return trend, nil
}

func (s *Service) ListEvalResults(ctx context.Context, ownerID, evalRunID int64) ([]workflow.EvalResult, error) {
	if s.evals == nil {
		return nil, fmt.Errorf("%w: eval repository is not configured", agenterrors.ErrInvalidInput)
	}
	if _, err := s.evals.FindEvalRunByID(ctx, ownerID, evalRunID); err != nil {
		return nil, mapNotFound(err)
	}
	return s.evals.ListEvalResultsByRun(ctx, ownerID, evalRunID)
}
