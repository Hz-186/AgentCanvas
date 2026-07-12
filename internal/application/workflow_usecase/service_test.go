package workflow_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/memory"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/sandbox"
	"agentcanvas/internal/runtime/toolruntime"

	"gorm.io/gorm"
)

func mustDefaultWorkflowNodes(t *testing.T, deps runtimenode.Deps) []engine.Node {
	t.Helper()
	if deps.ToolCalling == nil {
		if client, ok := deps.LLM.(llm.ToolCallingClient); ok {
			deps.ToolCalling = client
		}
	}
	if deps.Sandbox == nil {
		deps.Sandbox = sandbox.NewDockerRunner()
	}
	nodes, err := runtimenode.DefaultNodes(deps)
	if err != nil {
		t.Fatalf("DefaultNodes() error = %v", err)
	}
	return nodes
}

func TestCreateFlowVersionReusesEquivalentLatestVersion(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*workflow.WorkflowVersion{
		{ID: 10, OwnerID: 1, WorkflowID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`), IsDraft: true},
	}}
	service := newFlowVersionTestService(versions)

	// 位置和结构完全相同（仅 key 顺序不同），应复用已有版本
	created, err := service.CreateWorkflowVersion(context.Background(), 1, 20, CreateWorkflowVersionRequest{
		DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"},"_ui":{"x":120,"y":170}}}],"edges":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 10 || created.VersionNo != 1 {
		t.Fatalf("expected existing version, got %+v", created)
	}
	if versions.createCalls != 0 || versions.nextCalls != 0 {
		t.Fatalf("expected no new version, createCalls=%d nextCalls=%d", versions.createCalls, versions.nextCalls)
	}
}

func TestCreateFlowVersionCreatesVersionForPositionChange(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*workflow.WorkflowVersion{
		{ID: 10, OwnerID: 1, WorkflowID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`), IsDraft: true},
	}}
	service := newFlowVersionTestService(versions)

	// 逻辑不变，仅节点位置改变，应创建新版本
	created, err := service.CreateWorkflowVersion(context.Background(), 1, 20, CreateWorkflowVersionRequest{
		DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"},"_ui":{"x":480,"y":260}}}],"edges":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 10 || created.VersionNo != 2 {
		t.Fatalf("expected new v2 for position change, got %+v", created)
	}
	if versions.createCalls != 1 || versions.nextCalls != 1 {
		t.Fatalf("expected one new version, createCalls=%d nextCalls=%d", versions.createCalls, versions.nextCalls)
	}
}

func TestCreateFlowVersionCreatesVersionForRuntimeChange(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*workflow.WorkflowVersion{
		{ID: 10, OwnerID: 1, WorkflowID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"}}}],"edges":[]}`), IsPublished: true},
	}}
	service := newFlowVersionTestService(versions)

	created, err := service.CreateWorkflowVersion(context.Background(), 1, 20, CreateWorkflowVersionRequest{
		DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"question":"string"}}}],"edges":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 10 || created.VersionNo != 2 {
		t.Fatalf("expected new v2, got %+v", created)
	}
	if versions.createCalls != 1 || versions.nextCalls != 1 {
		t.Fatalf("expected one new version, createCalls=%d nextCalls=%d", versions.createCalls, versions.nextCalls)
	}
}

func TestRecordAgentStepPersistsRunStep(t *testing.T) {
	steps := &fakeRunStepRepo{}
	service := &Service{runSteps: steps}
	err := service.RecordAgentStep(context.Background(), &engine.RunContext{
		OwnerID:       1,
		RunID:         2,
		CurrentNodeID: "agent_loop",
	}, engine.AgentStepRecord{
		StepIndex:  3,
		StepType:   "tool_call",
		ToolCallID: "call_1",
		ToolName:   "search_knowledge",
		Content:    "content",
		Compressed: true,
		LatencyMS:  15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps.items) != 1 {
		t.Fatalf("expected one step, got %d", len(steps.items))
	}
	item := steps.items[0]
	if item.OwnerID != 1 || item.RunID != 2 || item.NodeID != "agent_loop" || item.StepIndex != 3 || item.ToolName != "search_knowledge" {
		t.Fatalf("unexpected step: %+v", item)
	}
	if !item.Compressed {
		t.Fatalf("expected compressed flag to be persisted: %+v", item)
	}
}

func TestGetAgentProfileCreatesDefaultProfile(t *testing.T) {
	profiles := &fakeProfileRepo{}
	service := &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "Researcher", Description: "Find facts", Status: workflow.StatusActive}}},
		profiles:  profiles,
	}
	profile, err := service.GetWorkflowProfile(context.Background(), 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Role != "Researcher" || profile.Goal != "Find facts" || profile.MaxIterations != 10 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if len(profiles.items) != 1 {
		t.Fatalf("expected default profile to be created, got %d", len(profiles.items))
	}
}

func TestUpdateAgentProfileValidatesLimits(t *testing.T) {
	profiles := &fakeProfileRepo{items: map[int64]*workflow.Profile{
		20: {ID: 1, OwnerID: 1, WorkflowID: 20, Role: "Old", Goal: "Old goal", MaxIterations: 10, MaxExecutionTimeMS: 120000},
	}}
	service := &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "Workflow", Status: workflow.StatusActive}}},
		profiles:  profiles,
	}
	tooMany := 99
	if _, err := service.UpdateWorkflowProfile(context.Background(), 1, 20, UpdateWorkflowProfileRequest{MaxIterations: &tooMany}); err == nil {
		t.Fatal("expected max_iterations validation error")
	}
	role := "Writer"
	goal := "Draft final answers"
	topK := 6
	mode := "hybrid"
	maxDepth := 3
	defaultPacks := []int64{8, 8, 0, 9}
	defaultTools := []int64{3, 3, 0, 4}
	defaultMCPServers := []int64{12, 13, 13}
	defaultKnowledge := []int64{10, 11}
	defaultAgents := []int64{21}
	outputSchema := rawJSON(`{"type":"object","required":["answer"]}`)
	toolPolicy := rawJSON(`{"require_approval_for_risk":["high"],"max_tool_timeout_ms":1500,"max_tool_output_bytes":4096,"allowed_hosts":["api.example.com"]}`)
	memoryPolicy := rawJSON(`{"enabled":true}`)
	contextPolicy := rawJSON(`{"max_input_tokens":12000}`)
	riskLevel := "high"
	profileMode := "plan_execute"
	updated, err := service.UpdateWorkflowProfile(context.Background(), 1, 20, UpdateWorkflowProfileRequest{
		Role:                        &role,
		Goal:                        &goal,
		DefaultToolPackIDs:          &defaultPacks,
		DefaultToolIDs:              &defaultTools,
		DefaultMCPServerIDs:         &defaultMCPServers,
		DefaultKnowledgeIDs:         &defaultKnowledge,
		DefaultKnowledgeTopK:        &topK,
		DefaultKnowledgeMode:        &mode,
		DefaultCallWorkflowIDs:      &defaultAgents,
		DefaultMaxWorkflowCallDepth: &maxDepth,
		OutputSchemaJSON:            &outputSchema,
		ToolPolicyJSON:              &toolPolicy,
		MemoryPolicyJSON:            &memoryPolicy,
		ContextPolicyJSON:           &contextPolicy,
		RiskLevel:                   &riskLevel,
		Mode:                        &profileMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != "Writer" || updated.Goal != "Draft final answers" || profiles.updateCalls != 1 {
		t.Fatalf("unexpected updated profile: %+v updateCalls=%d", updated, profiles.updateCalls)
	}
	if got := updated.DefaultToolIDsSlice(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("unexpected default tool ids: %+v raw=%s", got, string(updated.DefaultToolIDs))
	}
	if got := updated.DefaultToolPackIDsSlice(); len(got) != 2 || got[0] != 8 || got[1] != 9 {
		t.Fatalf("unexpected default tool pack ids: %+v raw=%s", got, string(updated.DefaultToolPackIDs))
	}
	if got := updated.DefaultMCPServerIDsSlice(); len(got) != 2 || got[0] != 12 || got[1] != 13 {
		t.Fatalf("unexpected default mcp server ids: %+v raw=%s", got, string(updated.DefaultMCPServerIDs))
	}
	if got := updated.DefaultKnowledgeIDsSlice(); len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("unexpected default knowledge ids: %+v", got)
	}
	if got := updated.DefaultCallWorkflowIDsSlice(); len(got) != 1 || got[0] != 21 {
		t.Fatalf("unexpected default call workflow ids: %+v", got)
	}
	if updated.DefaultKnowledgeTopK != 6 || updated.DefaultKnowledgeMode != "hybrid" || updated.DefaultMaxWorkflowCallDepth != 3 {
		t.Fatalf("unexpected profile defaults: %+v", updated)
	}
	if string(updated.OutputSchemaJSON) != `{"required":["answer"],"type":"object"}` {
		t.Fatalf("unexpected output schema: %s", string(updated.OutputSchemaJSON))
	}
	if string(updated.ToolPolicyJSON) != `{"allowed_hosts":["api.example.com"],"max_tool_output_bytes":4096,"max_tool_timeout_ms":1500,"require_approval_for_risk":["high"]}` {
		t.Fatalf("unexpected tool policy: %s", string(updated.ToolPolicyJSON))
	}
	if string(updated.MemoryPolicyJSON) != `{"enabled":true}` {
		t.Fatalf("unexpected memory policy: %s", string(updated.MemoryPolicyJSON))
	}
	if string(updated.ContextPolicyJSON) != `{"max_input_tokens":12000}` || updated.RiskLevel != "high" || updated.Mode != "plan_execute" {
		t.Fatalf("unexpected profile policy fields: %+v context=%s", updated, string(updated.ContextPolicyJSON))
	}
	badTopK := 99
	if _, err := service.UpdateWorkflowProfile(context.Background(), 1, 20, UpdateWorkflowProfileRequest{DefaultKnowledgeTopK: &badTopK}); err == nil {
		t.Fatal("expected default_knowledge_top_k validation error")
	}
	badMode := "invalid"
	if _, err := service.UpdateWorkflowProfile(context.Background(), 1, 20, UpdateWorkflowProfileRequest{Mode: &badMode}); err == nil {
		t.Fatal("expected invalid mode validation error")
	}
	invalidRules := rawJSON(`{"rules":[{"id":"tenant.core","level":"l1_core","content":"override"}]}`)
	if _, err := service.UpdateWorkflowProfile(context.Background(), 1, 20, UpdateWorkflowProfileRequest{ContextPolicyJSON: &invalidRules}); err == nil {
		t.Fatal("expected permanent custom rule validation error")
	}
	validRules := rawJSON(`{"rule_set_version":"release-2026-07","rules":[{"id":"tenant.release.check","level":"l2_scenario","content":"require rollback","activation":{"tag_any":["release"]}}]}`)
	updated, err = service.UpdateWorkflowProfile(context.Background(), 1, 20, UpdateWorkflowProfileRequest{ContextPolicyJSON: &validRules})
	if err != nil || !strings.Contains(string(updated.ContextPolicyJSON), "tenant.release.check") {
		t.Fatalf("expected persisted valid custom rules, profile=%+v err=%v", updated, err)
	}
}

func TestCreateEvalDatasetPersistsDataset(t *testing.T) {
	evals := &fakeEvalRepo{}
	service := &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "Workflow", Status: workflow.StatusActive}}},
		evals:     evals,
	}

	dataset, err := service.CreateEvalDataset(context.Background(), 1, 20, CreateEvalDatasetRequest{
		Name:        " Regression ",
		Description: "core cases",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dataset.ID == 0 || dataset.Name != "Regression" || dataset.WorkflowID != 20 || dataset.Status != workflow.EvalDatasetStatusActive {
		t.Fatalf("unexpected dataset: %+v", dataset)
	}
	if len(evals.datasets) != 1 {
		t.Fatalf("expected one dataset, got %d", len(evals.datasets))
	}
}

func TestCreateEvalCaseNormalizesJSON(t *testing.T) {
	evals := &fakeEvalRepo{
		datasets: []*workflow.EvalDataset{{ID: 30, OwnerID: 1, WorkflowID: 20, Name: "Regression", Status: workflow.EvalDatasetStatusActive}},
	}
	service := &Service{evals: evals}

	item, err := service.CreateEvalCase(context.Background(), 1, 30, CreateEvalCaseRequest{
		Name:              "RAG smoke",
		InputJSON:         rawJSON(`{"query":"hello"}`),
		ExpectedJSON:      rawJSON(`{"contains":"world"}`),
		TagsJSON:          rawJSON(`["rag","smoke"]`),
		RequiredToolsJSON: rawJSON(`["search_knowledge"]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == 0 || item.DatasetID != 30 || string(item.InputJSON) != `{"query":"hello"}` {
		t.Fatalf("unexpected eval case: %+v", item)
	}
	if len(evals.cases) != 1 {
		t.Fatalf("expected one case, got %d", len(evals.cases))
	}
}

func TestCreateEvalCaseRejectsNonObjectInput(t *testing.T) {
	evals := &fakeEvalRepo{
		datasets: []*workflow.EvalDataset{{ID: 30, OwnerID: 1, WorkflowID: 20, Name: "Regression", Status: workflow.EvalDatasetStatusActive}},
	}
	service := &Service{evals: evals}

	if _, err := service.CreateEvalCase(context.Background(), 1, 30, CreateEvalCaseRequest{
		Name:      "bad case",
		InputJSON: rawJSON(`["not-object"]`),
	}); err == nil {
		t.Fatal("expected non-object input_json validation error")
	}
}

func TestScoreEvalOutputSupportsContainsArray(t *testing.T) {
	score, reason := scoreEvalOutput(engine.NodeOutput{"content": "alpha beta gamma"}, rawJSON(`{"contains":["alpha","gamma"]}`), nil)
	if score != 1 {
		t.Fatalf("expected contains array to pass, score=%v reason=%s", score, reason)
	}
	score, reason = scoreEvalOutput(engine.NodeOutput{"content": "alpha beta"}, rawJSON(`{"contains":["alpha","gamma"]}`), nil)
	if score != 0 || !strings.Contains(reason, "gamma") {
		t.Fatalf("expected missing array item to fail, score=%v reason=%s", score, reason)
	}
}

func TestScoreEvalOutputDetailedReportsToolAndSchemaMetrics(t *testing.T) {
	output := engine.NodeOutput{
		"final_answer":      `{"answer":"ok"}`,
		"structured_output": map[string]any{"answer": "ok"},
		"stop_reason":       runtimeagent.StopReasonPlanCompleted,
		"total_tokens":      42,
		"latency_ms":        120,
		"steps": []runtimeagent.RunStep{
			{Type: runtimeagent.StepTypeToolCall, ToolName: "search_knowledge"},
			{Type: runtimeagent.StepTypeReflection},
		},
	}
	result := scoreEvalOutputDetailed(
		output,
		rawJSON(`{"json_schema":{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}},"contains":"ok"}`),
		rawJSON(`["search_knowledge"]`),
	)
	if result.Score != 1 {
		t.Fatalf("expected eval to pass: %+v", result)
	}
	if result.Metrics["tool_call_accuracy"] != 1.0 || result.Metrics["schema_compliance"] != 1.0 || result.Metrics["total_tokens"] != 42 {
		t.Fatalf("unexpected metrics: %+v", result.Metrics)
	}
	if result.Metrics["reflection_repair_attempted"] != true {
		t.Fatalf("expected reflection metric, got %+v", result.Metrics)
	}
}

func TestScoreEvalOutputDetailedReportsRAGAndTokenSavedMetrics(t *testing.T) {
	result := scoreEvalOutputDetailed(
		engine.NodeOutput{
			"final_answer":  "ok with citations",
			"context_trace": map[string]any{"saved_tokens": 512, "provider_prompt_tokens": 900, "token_estimation_error": 40, "rule_set_version": "release-2026-07", "rule_rounds": []any{map[string]any{}, map[string]any{}}, "rule_trace": map[string]any{"estimated_used": 80}, "rule_budget": map[string]any{"available_rule_tokens": 220}},
			"results": []map[string]any{
				{"document_id": "doc-a", "chunk_id": "chunk-a", "score": 0.92, "content": "source a"},
				{"document_id": "doc-b", "chunk_id": "chunk-b", "score": 0.81, "content": "source b"},
			},
		},
		rawJSON(`{"contains":"ok","expected_doc_ids":["doc-b"],"required_citations":["chunk-b"]}`),
		nil,
	)
	if result.Score != 1 {
		t.Fatalf("expected eval to pass: %+v", result)
	}
	if result.Metrics["token_saved"] != 512 || result.Metrics["rules_token_cost"] != float64(80) || result.Metrics["rules_dynamic_budget"] != float64(220) || result.Metrics["provider_prompt_tokens"] != float64(900) || result.Metrics["rule_round_count"] != 2 || result.Metrics["rule_set_version"] != "release-2026-07" || result.Metrics["retrieval_hit_rate"] != 1.0 || result.Metrics["mrr"] != 0.5 || result.Metrics["citation_rate"] != 1.0 {
		t.Fatalf("expected RAG and token saved metrics, got %+v", result.Metrics)
	}
}

func TestScoreEvalOutputTreatsCallAgentAndCallWorkflowAsAliases(t *testing.T) {
	result := scoreEvalOutputDetailed(
		engine.NodeOutput{
			"final_answer": "ok",
			"steps": []runtimeagent.RunStep{
				{Type: runtimeagent.StepTypeToolCall, ToolName: "call_agent"},
			},
		},
		rawJSON(`{"contains":"ok"}`),
		rawJSON(`["call_workflow"]`),
	)
	if result.Score != 1 || result.Metrics["tool_call_accuracy"] != 1.0 {
		t.Fatalf("expected call_agent to satisfy legacy call_workflow requirement, got %+v", result)
	}
}

func TestScoreEvalOutputDetailedFailsSchemaMismatch(t *testing.T) {
	result := scoreEvalOutputDetailed(
		engine.NodeOutput{"final_answer": `{"answer":7}`},
		rawJSON(`{"json_schema":{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}}`),
		nil,
	)
	if result.Score != 0 || result.Metrics["schema_compliance"] != 0.0 {
		t.Fatalf("expected schema mismatch failure, got %+v", result)
	}
}

func TestScoreEvalOutputDetailedRequiresReferences(t *testing.T) {
	missing := scoreEvalOutputDetailed(
		engine.NodeOutput{"final_answer": "ok"},
		rawJSON(`{"contains":"ok","require_references":true}`),
		nil,
	)
	if missing.Score != 0 || missing.Metrics["reference_hit_rate"] != 0.0 {
		t.Fatalf("expected missing references to fail, got %+v", missing)
	}

	withRefs := scoreEvalOutputDetailed(
		engine.NodeOutput{
			"final_answer": "ok",
			"results": []map[string]any{
				{"document_id": 10, "chunk_id": 20, "content": "source"},
			},
		},
		rawJSON(`{"contains":"ok","min_references":1}`),
		nil,
	)
	if withRefs.Score != 1 || withRefs.Metrics["reference_count"] != 1 || withRefs.Metrics["reference_hit_rate"] != 1.0 {
		t.Fatalf("expected references to satisfy eval, got %+v", withRefs)
	}
}

func TestCallAgentRejectsIndirectCycle(t *testing.T) {
	events := &fakeRunEventRepo{}
	service := &Service{events: events}

	_, err := service.CallWorkflow(context.Background(), toolruntime.WorkflowCallRequest{
		OwnerID:           1,
		ParentRunID:       99,
		CallerWorkflowID:  21,
		CallerNodeID:      "call_workflow",
		WorkflowID:        20,
		Input:             map[string]any{"query": "loop"},
		WorkflowCallChain: []int64{20, 21},
	})
	if !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("expected forbidden cycle error, got %v", err)
	}
	if len(events.items) != 1 {
		t.Fatalf("expected one blocked call event, got %d", len(events.items))
	}
	var payload map[string]any
	if err := json.Unmarshal(events.items[0].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if events.items[0].RunID != 99 || events.items[0].EventType != "workflow_call_failed" || payload["blocked_reason"] != "workflow_call_cycle_detected" {
		t.Fatalf("unexpected blocked event: %+v payload=%+v", events.items[0], payload)
	}
}

func TestListChildRunsReturnsRunsByParent(t *testing.T) {
	parentID := int64(10)
	otherParentID := int64(99)
	runs := &fakeRunRepo{items: []*workflow.Run{
		{ID: parentID, OwnerID: 1, WorkflowID: 20, Status: workflow.RunStatusSucceeded},
		{ID: 11, OwnerID: 1, WorkflowID: 21, ParentRunID: &parentID, Status: workflow.RunStatusSucceeded},
		{ID: 12, OwnerID: 1, WorkflowID: 22, ParentRunID: &parentID, Status: workflow.RunStatusFailed},
		{ID: 13, OwnerID: 1, WorkflowID: 23, ParentRunID: &otherParentID, Status: workflow.RunStatusSucceeded},
		{ID: 14, OwnerID: 2, WorkflowID: 21, ParentRunID: &parentID, Status: workflow.RunStatusSucceeded},
	}}
	service := &Service{runs: runs}

	items, err := service.ListChildRuns(context.Background(), 1, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != 11 || items[1].ID != 12 {
		t.Fatalf("unexpected child runs: %+v", items)
	}
}

func TestGetRunTraceAggregatesReplayArtifacts(t *testing.T) {
	parentID := int64(10)
	childID := int64(11)
	runs := &fakeRunRepo{items: []*workflow.Run{
		{ID: parentID, OwnerID: 1, WorkflowID: 20, Status: workflow.RunStatusSucceeded, LatencyMS: 123, TotalTokens: 42},
		{ID: childID, OwnerID: 1, WorkflowID: 21, ParentRunID: &parentID, Status: workflow.RunStatusSucceeded},
	}}
	service := &Service{
		runs: runs,
		events: &fakeRunEventRepo{items: []workflow.RunEvent{
			{ID: 1, OwnerID: 1, RunID: parentID, EventType: "node_started"},
		}},
		nodeLogs: &fakeNodeLogRepo{items: []workflow.NodeLog{
			{ID: 2, OwnerID: 1, RunID: parentID, NodeID: "agent_loop"},
		}},
		runSteps: &fakeRunStepRepo{items: []workflow.RunStep{
			{ID: 3, OwnerID: 1, RunID: parentID, StepType: runtimeagent.StepTypeToolCall, ToolName: "search_knowledge"},
			{ID: 4, OwnerID: 1, RunID: parentID, StepType: runtimeagent.StepTypeReflection, Compressed: true},
		}},
		memoryLogs: &fakeMemoryWriteLogRepo{items: []memory.WriteLog{
			{ID: 5, OwnerID: 1, RunID: parentID, Action: "create"},
		}},
		toolInvocations: &fakeToolInvocationRepo{items: []tool.Invocation{
			{ID: 6, OwnerID: 1, RunID: parentID, ToolName: "search_knowledge"},
		}},
	}

	trace, err := service.GetRunTrace(context.Background(), 1, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if trace.Run.ID != parentID || len(trace.Events) != 1 || len(trace.NodeLogs) != 1 || len(trace.Steps) != 2 || len(trace.ChildRuns) != 1 || len(trace.MemoryWriteLogs) != 1 || len(trace.ToolInvocations) != 1 {
		t.Fatalf("unexpected trace aggregate: %+v", trace)
	}
	if trace.ReplaySummary["compressed_step_count"] != 1 || trace.ReplaySummary["reflection_step_count"] != 1 || trace.ReplaySummary["tool_call_step_count"] != 1 || trace.ReplaySummary["child_run_count"] != 1 {
		t.Fatalf("unexpected replay summary: %+v", trace.ReplaySummary)
	}
}

func TestServiceCreatesTeamAndMembers(t *testing.T) {
	teams := &fakeTeamRepo{}
	service := &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{
			20: {ID: 20, OwnerID: 1, Name: "Supervisor", Status: workflow.StatusActive},
			21: {ID: 21, OwnerID: 1, Name: "Researcher", Status: workflow.StatusActive},
		}},
		teams: teams,
	}

	team, err := service.CreateTeam(context.Background(), 1, CreateTeamRequest{
		Name:                 "Research Team",
		SupervisorWorkflowID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if team.ID == 0 || team.HandoffStrategy != "supervisor" || team.MaxDepth != 3 {
		t.Fatalf("unexpected team: %+v", team)
	}
	handoffTeam, err := service.CreateTeam(context.Background(), 1, CreateTeamRequest{
		Name:                 "Handoff Team",
		SupervisorWorkflowID: 20,
		HandoffStrategy:      "handoff",
		MaxDepth:             4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if handoffTeam.HandoffStrategy != "handoff" || handoffTeam.MaxDepth != 4 {
		t.Fatalf("unexpected handoff team: %+v", handoffTeam)
	}
	member, err := service.AddTeamMember(context.Background(), 1, team.ID, AddTeamMemberRequest{WorkflowID: 21, Role: "research"})
	if err != nil {
		t.Fatal(err)
	}
	if member.ID == 0 || member.TeamID != team.ID || member.Role != "research" {
		t.Fatalf("unexpected member: %+v", member)
	}
	members, err := service.ListTeamMembers(context.Background(), 1, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].WorkflowID != 21 {
		t.Fatalf("unexpected members: %+v", members)
	}
}

func TestRunEvalDatasetExecutesCasesAndScoresOutput(t *testing.T) {
	evals := &fakeEvalRepo{
		datasets: []*workflow.EvalDataset{{ID: 30, OwnerID: 1, WorkflowID: 20, Name: "Regression", Status: workflow.EvalDatasetStatusActive}},
		cases: []*workflow.EvalCase{{
			ID:           200,
			OwnerID:      1,
			DatasetID:    30,
			Name:         "begin passthrough",
			InputJSON:    rawJSON(`{"final_answer":"hello world"}`),
			ExpectedJSON: rawJSON(`{"contains":"world"}`),
		}},
	}
	versions := &fakeFlowVersionRepo{items: []*workflow.WorkflowVersion{{
		ID:          40,
		OwnerID:     1,
		WorkflowID:  20,
		VersionNo:   1,
		DSLJSON:     rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{}}],"edges":[]}`),
		IsPublished: true,
	}}}
	service := &Service{
		workflows:  &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "Workflow", Status: workflow.StatusActive}}},
		versions:   versions,
		runs:       &fakeRunRepo{},
		events:     &fakeRunEventRepo{},
		nodeLogs:   &fakeNodeLogRepo{},
		evals:      evals,
		executor:   engine.NewExecutor(mustDefaultWorkflowNodes(t, runtimenode.Deps{LLM: &fakeResumeLLM{}})),
		validator:  flow.NewValidator(engine.NewExecutor(mustDefaultWorkflowNodes(t, runtimenode.Deps{LLM: &fakeResumeLLM{}}))),
		runCancels: newRunCancelRegistry(),
	}

	evalRun, results, err := service.RunEvalDataset(context.Background(), 1, 30, RunEvalDatasetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if evalRun.Status != workflow.EvalRunStatusCompleted || evalRun.PassedCases != 1 || evalRun.SuccessRate != 1 {
		t.Fatalf("unexpected eval run: %+v", evalRun)
	}
	if len(results) != 1 || results[0].Status != "passed" || results[0].Score != 1 {
		t.Fatalf("unexpected eval results: %+v", results)
	}
	if len(evals.evalRuns) != 1 || len(evals.results) != 1 {
		t.Fatalf("expected persisted eval run/result, got %d/%d", len(evals.evalRuns), len(evals.results))
	}
	var metrics map[string]any
	if err := json.Unmarshal(evals.results[0].MetricsJSON, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["tool_call_accuracy"] != float64(1) || metrics["schema_compliance"] != float64(1) {
		t.Fatalf("expected detailed eval metrics, got %+v", metrics)
	}
	var summary struct {
		Metrics map[string]float64 `json:"metrics"`
	}
	if err := json.Unmarshal(evals.evalRuns[0].SummaryJSON, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Metrics["avg_score"] != 1 || summary.Metrics["avg_tool_call_accuracy"] != 1 || summary.Metrics["avg_schema_compliance"] != 1 {
		t.Fatalf("expected eval run metric summary, got %+v raw=%s", summary.Metrics, string(evals.evalRuns[0].SummaryJSON))
	}
}

func TestGetEvalTrendAggregatesRunHistory(t *testing.T) {
	started := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	evals := &fakeEvalRepo{
		datasets: []*workflow.EvalDataset{{ID: 30, OwnerID: 1, WorkflowID: 20, Name: "Regression", Status: workflow.EvalDatasetStatusActive}},
		evalRuns: []*workflow.EvalRun{
			{
				ID:            401,
				OwnerID:       1,
				WorkflowID:    20,
				DatasetID:     30,
				FlowVersionID: 42,
				Status:        workflow.EvalRunStatusCompleted,
				TotalCases:    4,
				PassedCases:   2,
				FailedCases:   2,
				SuccessRate:   0.5,
				SummaryJSON:   rawJSON(`{"metrics":{"avg_score":0.5,"avg_tool_call_accuracy":0.25,"avg_latency_ms":100}}`),
				StartedAt:     started,
				FinishedAt:    &finished,
			},
			{
				ID:            402,
				OwnerID:       1,
				WorkflowID:    20,
				DatasetID:     30,
				FlowVersionID: 43,
				Status:        workflow.EvalRunStatusCompleted,
				TotalCases:    4,
				PassedCases:   3,
				FailedCases:   1,
				SuccessRate:   0.75,
				SummaryJSON:   rawJSON(`{"metrics":{"avg_score":0.75,"avg_tool_call_accuracy":0.75,"avg_latency_ms":80}}`),
				StartedAt:     started.Add(time.Hour),
				FinishedAt:    &finished,
			},
		},
	}
	service := &Service{evals: evals}

	trend, err := service.GetEvalTrend(context.Background(), 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if trend.DatasetID != 30 || trend.WorkflowID != 20 || len(trend.Points) != 2 {
		t.Fatalf("unexpected trend envelope: %+v", trend)
	}
	if trend.Latest == nil || trend.Latest.EvalRunID != 402 || trend.Best == nil || trend.Best.EvalRunID != 402 {
		t.Fatalf("unexpected latest/best: latest=%+v best=%+v", trend.Latest, trend.Best)
	}
	if trend.Delta["success_rate"] != 0.25 || trend.Delta["avg_score"] != 0.25 || trend.Delta["avg_tool_call_accuracy"] != 0.5 || trend.Delta["avg_latency_ms"] != -20.0 {
		t.Fatalf("unexpected trend delta: %+v", trend.Delta)
	}
	if trend.TrendSummary["run_count"] != 2 || trend.TrendSummary["best_success_rate"] != 0.75 {
		t.Fatalf("unexpected trend summary: %+v", trend.TrendSummary)
	}
}

func TestSummarizeEvalMetricsCountsMissingMetricsAsZero(t *testing.T) {
	results := []workflow.EvalResult{
		{Status: "passed", Score: 1, LatencyMS: 20, MetricsJSON: rawJSON(`{"tool_call_accuracy":1,"schema_compliance":1,"reference_hit_rate":1,"retrieval_hit_rate":1,"mrr":0.5,"ndcg":0.75,"citation_rate":1,"token_saved":100,"rules_token_cost":80,"provider_prompt_tokens":500,"human_approval_waiting":true}`)},
		{Status: "failed", Score: 0, LatencyMS: 40, ErrorMessage: "runtime error", MetricsJSON: rawJSON(`{"score":0}`)},
	}
	summary := summarizeEvalMetrics(results)
	if summary["avg_score"] != 0.5 || summary["avg_tool_call_accuracy"] != 0.5 || summary["avg_schema_compliance"] != 0.5 || summary["avg_reference_hit_rate"] != 0.5 {
		t.Fatalf("expected missing metrics to count as zero, got %+v", summary)
	}
	if summary["avg_latency_ms"] != float64(30) || summary["human_approval_waiting_rate"] != 0.5 || summary["failed_cases_with_runtime_error_rate"] != 0.5 {
		t.Fatalf("unexpected eval metric summary: %+v", summary)
	}
	if summary["avg_retrieval_hit_rate"] != 0.5 || summary["avg_mrr"] != 0.25 || summary["avg_ndcg"] != 0.375 || summary["avg_citation_rate"] != 0.5 || summary["avg_token_saved"] != float64(50) {
		t.Fatalf("expected RAG/token metrics in summary, got %+v", summary)
	}
	if summary["avg_rules_token_cost"] != float64(40) || summary["avg_provider_prompt_tokens"] != float64(250) {
		t.Fatalf("expected rules cost metrics in summary: %+v", summary)
	}
	versions := summarizeRuleSetVersions(results)
	builtin, _ := versions["builtin"].(map[string]any)
	if builtin["cases"] != 2 || builtin["avg_rules_token_cost"] != float64(40) {
		t.Fatalf("expected version-level rule metrics: %+v", versions)
	}
}

func TestPersistWaitingHumanArtifactsStoresApprovalAndCheckpoint(t *testing.T) {
	approvals := &fakeApprovalRepo{}
	service := &Service{approvals: approvals}
	output := engine.NodeOutput{
		"approval": map[string]any{
			"tool_call_id": "call_1",
			"tool_name":    "send_email",
			"risk_level":   "high",
			"reason":       "high risk tool requires approval",
		},
		"checkpoint": map[string]any{
			"messages":          []map[string]any{{"role": "user", "content": "send it"}},
			"messages_summary":  "user: send it",
			"pending_tool_call": map[string]any{"id": "call_1", "name": "send_email", "arguments": map[string]any{"to": "a@example.com"}},
			"context":           map[string]any{"strategy": "priority_budget_sliding_window"},
			"tool_policy":       map[string]any{"require_approval_for_risk": []string{"high"}},
			"tool_names":        []string{"send_email"},
			"metadata":          map[string]any{"node_id": "agent", "iteration": 2, "tool_calls": 1},
		},
	}
	err := service.persistRunCheckpointArtifacts(context.Background(), &workflow.Run{ID: 7, OwnerID: 1, WorkflowID: 20}, output, workflow.RunStatusWaitingHuman)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals.requests) != 1 || approvals.requests[0].ToolName != "send_email" || approvals.requests[0].Status != workflow.ApprovalStatusPending {
		t.Fatalf("unexpected approval request: %+v", approvals.requests)
	}
	if len(approvals.checkpoints) != 1 || len(approvals.checkpoints[0].MessagesJSON) == 0 || approvals.checkpoints[0].ToolPolicyHash == "" {
		t.Fatalf("unexpected checkpoint: %+v", approvals.checkpoints)
	}
	var envelope checkpointContextEnvelope
	if err := json.Unmarshal(approvals.checkpoints[0].ContextJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Context.Strategy != "priority_budget_sliding_window" || envelope.Metadata["iteration"] != float64(2) || envelope.Metadata["tool_calls"] != float64(1) {
		t.Fatalf("checkpoint context envelope lost metadata: %+v", envelope)
	}
	decoded, err := decodeRuntimeCheckpoint(approvals.checkpoints[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Metadata["iteration"] != float64(2) || decoded.Metadata["tool_calls"] != float64(1) {
		t.Fatalf("decoded checkpoint lost metadata: %+v", decoded.Metadata)
	}
	if decoded.Metadata["tool_registry_hash"] == "" || decoded.Metadata["tool_policy_hash"] == "" {
		t.Fatalf("decoded checkpoint should expose hashes: %+v", decoded.Metadata)
	}
}

func TestPersistPausedArtifactsStoresCheckpointWithoutApproval(t *testing.T) {
	approvals := &fakeApprovalRepo{}
	service := &Service{approvals: approvals}
	output := engine.NodeOutput{
		"checkpoint": map[string]any{
			"messages":         []map[string]any{{"role": "user", "content": "pause"}},
			"messages_summary": "user: pause",
			"context":          map[string]any{"strategy": "priority_budget_sliding_window"},
			"tool_names":       []string{"search_knowledge"},
			"metadata":         map[string]any{"node_id": "agent"},
		},
	}
	err := service.persistRunCheckpointArtifacts(context.Background(), &workflow.Run{ID: 7, OwnerID: 1, WorkflowID: 20}, output, workflow.RunStatusPaused)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals.requests) != 0 {
		t.Fatalf("paused checkpoint should not create approval: %+v", approvals.requests)
	}
	if len(approvals.checkpoints) != 1 || approvals.checkpoints[0].Status != workflow.RunStatusPaused || len(approvals.checkpoints[0].MessagesJSON) == 0 {
		t.Fatalf("unexpected checkpoint: %+v", approvals.checkpoints)
	}
}

func TestPersistPausedArtifactsPreservesCheckpointHashesFromMetadata(t *testing.T) {
	approvals := &fakeApprovalRepo{}
	service := &Service{approvals: approvals}
	output := engine.NodeOutput{
		"checkpoint": &runtimeagent.Checkpoint{
			MessagesSummary: "paused",
			Metadata: map[string]any{
				"node_id":            "agent",
				"tool_registry_hash": "stored-registry",
				"tool_policy_hash":   "stored-policy",
			},
		},
	}
	err := service.persistRunCheckpointArtifacts(context.Background(), &workflow.Run{ID: 7, OwnerID: 1, WorkflowID: 20}, output, workflow.RunStatusPaused)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals.checkpoints) != 1 {
		t.Fatalf("expected one checkpoint, got %d", len(approvals.checkpoints))
	}
	if approvals.checkpoints[0].ToolRegistryHash != "stored-registry" || approvals.checkpoints[0].ToolPolicyHash != "stored-policy" {
		t.Fatalf("expected stored hashes to be preserved, got %+v", approvals.checkpoints[0])
	}
}

func TestResumeRunContinuesFromCheckpointAfterApproval(t *testing.T) {
	secretBox, err := cryptoinfra.NewSecretBox("resume-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	encryptedKey, err := secretBox.Encrypt("test-key")
	if err != nil {
		t.Fatal(err)
	}
	llmClient := &fakeResumeLLM{responses: []llm.ToolChatResponse{{
		Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "resumed final answer"},
		Usage:   llm.Usage{TotalTokens: 5},
	}}}
	dangerousTool := &fakeResumeTool{name: "dangerous_tool", output: "tool executed"}
	runs := &fakeRunRepo{items: []*workflow.Run{{
		ID:             900,
		OwnerID:        1,
		WorkflowID:     20,
		FlowVersionID:  40,
		Status:         workflow.RunStatusWaitingHuman,
		InputJSON:      rawJSON(`{"query":"please execute"}`),
		CallChainJSON:  rawJSON(`[20]`),
		StartedAt:      time.Now().UTC().Add(-time.Second),
		ConversationID: nil,
	}}}
	approvals := &fakeApprovalRepo{
		requests: []*workflow.ApprovalRequest{{
			ID:           601,
			OwnerID:      1,
			WorkflowID:   20,
			RunID:        900,
			NodeID:       "agent_loop_1",
			ToolCallID:   "call_1",
			ToolName:     "dangerous_tool",
			RiskLevel:    "high",
			Status:       workflow.ApprovalStatusApproved,
			DecisionNote: "approved",
		}},
		checkpoints: []*workflow.WorkflowCheckpoint{{
			ID:                  701,
			OwnerID:             1,
			WorkflowID:          20,
			RunID:               900,
			NodeID:              "agent_loop_1",
			Status:              workflow.RunStatusWaitingHuman,
			MessagesJSON:        mustJSON([]llm.ChatMessage{{Role: conversation.RoleUser, Content: "please execute"}, {Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "dangerous_tool", Arguments: json.RawMessage(`{"x":1}`)}}}}),
			PendingToolCallJSON: mustJSON(llm.ToolCall{ID: "call_1", Name: "dangerous_tool", Arguments: json.RawMessage(`{"x":1}`)}),
			MessagesSummary:     "assistant requested dangerous_tool",
		}},
	}
	service := &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "Workflow", Status: workflow.StatusActive}}},
		profiles: &fakeProfileRepo{items: map[int64]*workflow.Profile{20: {
			ID:                 1,
			OwnerID:            1,
			WorkflowID:         20,
			Role:               "Workflow",
			Goal:               "Resume work",
			MaxIterations:      3,
			MaxExecutionTimeMS: 120000,
		}}},
		versions: &fakeFlowVersionRepo{items: []*workflow.WorkflowVersion{{
			ID:         40,
			OwnerID:    1,
			WorkflowID: 20,
			VersionNo:  1,
			DSLJSON:    rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"agent_loop_1","type":"agent_loop","name":"Workflow Loop","config":{"provider_id":1,"model":"gpt-test","task_template":"{{sys.query}}","tool_ids":[1],"max_iterations":3,"max_tool_calls":3,"max_execution_time_ms":120000,"require_approval_for_risk":["high"]}}],"edges":[]}`),
		}}},
		runs:         runs,
		runSteps:     &fakeRunStepRepo{},
		approvals:    approvals,
		providers:    &fakeProviderRepo{items: map[int64]*providerdomain.ModelProvider{1: {ID: 1, OwnerID: 1, ProviderType: providerdomain.TypeOpenAICompatible, BaseURL: "https://example.test", EncryptedAPIKey: encryptedKey, DefaultChatModel: "gpt-test", Status: providerdomain.StatusActive}}},
		llm:          llmClient,
		secrets:      secretBox,
		toolRegistry: fakeToolRegistry{tools: []toolruntime.RuntimeTool{dangerousTool}},
	}

	resumed, err := service.ResumeRun(context.Background(), 1, 900)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != workflow.RunStatusSucceeded || !jsonContains(resumed.OutputJSON, "resumed final answer") {
		t.Fatalf("unexpected resumed run: %+v output=%s", resumed, string(resumed.OutputJSON))
	}
	if string(dangerousTool.input) != `{"x":1}` {
		t.Fatalf("expected pending tool to execute, got input %s", string(dangerousTool.input))
	}
	if len(llmClient.requests) != 1 || !messagesContainToolResult(llmClient.requests[0].Messages, "call_1", "tool executed") {
		t.Fatalf("expected LLM to receive resumed tool result, requests=%+v", llmClient.requests)
	}
}

func newFlowVersionTestService(versions *fakeFlowVersionRepo) *Service {
	return &Service{
		workflows: &fakeAgentRepo{items: map[int64]*workflow.Workflow{20: {ID: 20, OwnerID: 1, Name: "test", Status: workflow.StatusActive}}},
		versions:  versions,
		validator: flow.NewValidator(nil),
	}
}

func rawJSON(raw string) json.RawMessage {
	return json.RawMessage(raw)
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

type fakeAgentRepo struct {
	items map[int64]*workflow.Workflow
}

func (r *fakeAgentRepo) Create(context.Context, *workflow.Workflow) error { return nil }

func (r *fakeAgentRepo) ListByOwner(context.Context, int64) ([]workflow.Workflow, error) {
	return nil, nil
}

func (r *fakeAgentRepo) FindByID(_ context.Context, ownerID, id int64) (*workflow.Workflow, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeAgentRepo) Update(context.Context, *workflow.Workflow) error { return nil }

func (r *fakeAgentRepo) SoftDelete(context.Context, int64, int64) error { return nil }

type fakeFlowVersionRepo struct {
	items       []*workflow.WorkflowVersion
	createCalls int
	nextCalls   int
}

func (r *fakeFlowVersionRepo) Create(_ context.Context, item *workflow.WorkflowVersion) error {
	r.createCalls++
	clone := *item
	clone.ID = int64(100 + r.createCalls)
	r.items = append(r.items, &clone)
	*item = clone
	return nil
}

func (r *fakeFlowVersionRepo) ListByWorkflow(_ context.Context, ownerID, workflowID int64) ([]workflow.WorkflowVersion, error) {
	items := make([]workflow.WorkflowVersion, 0, len(r.items))
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.WorkflowID == workflowID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeFlowVersionRepo) FindByID(_ context.Context, ownerID, id int64) (*workflow.WorkflowVersion, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeFlowVersionRepo) FindCurrentByWorkflow(_ context.Context, ownerID, workflowID int64) (*workflow.WorkflowVersion, error) {
	var current *workflow.WorkflowVersion
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.WorkflowID == workflowID && item.IsPublished && (current == nil || item.VersionNo > current.VersionNo) {
			current = item
		}
	}
	if current == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *current
	return &clone, nil
}

func (r *fakeFlowVersionRepo) FindLatestByWorkflow(_ context.Context, ownerID, workflowID int64) (*workflow.WorkflowVersion, error) {
	var latest *workflow.WorkflowVersion
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.WorkflowID == workflowID && (latest == nil || item.VersionNo > latest.VersionNo) {
			latest = item
		}
	}
	if latest == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *latest
	return &clone, nil
}

func (r *fakeFlowVersionRepo) NextVersionNo(_ context.Context, ownerID, workflowID int64) (int, error) {
	r.nextCalls++
	maxVersion := 0
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.WorkflowID == workflowID && item.VersionNo > maxVersion {
			maxVersion = item.VersionNo
		}
	}
	return maxVersion + 1, nil
}

func (r *fakeFlowVersionRepo) Publish(context.Context, int64, int64, int64) error { return nil }

type fakeProfileRepo struct {
	items       map[int64]*workflow.Profile
	updateCalls int
}

func (r *fakeProfileRepo) Create(_ context.Context, item *workflow.Profile) error {
	if r.items == nil {
		r.items = map[int64]*workflow.Profile{}
	}
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(len(r.items) + 1)
	}
	r.items[item.WorkflowID] = &clone
	*item = clone
	return nil
}

func (r *fakeProfileRepo) FindByWorkflow(_ context.Context, ownerID, workflowID int64) (*workflow.Profile, error) {
	item, ok := r.items[workflowID]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeProfileRepo) Update(_ context.Context, item *workflow.Profile) error {
	r.updateCalls++
	if r.items == nil {
		r.items = map[int64]*workflow.Profile{}
	}
	clone := *item
	r.items[item.WorkflowID] = &clone
	return nil
}

type fakeRunStepRepo struct {
	items []workflow.RunStep
}

type fakeMemoryWriteLogRepo struct {
	items []memory.WriteLog
}

type fakeToolInvocationRepo struct {
	items []tool.Invocation
}

type fakeRunRepo struct {
	items []*workflow.Run
}

func (r *fakeRunRepo) Create(_ context.Context, item *workflow.Run) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(300 + len(r.items))
	}
	if clone.CreatedAt.IsZero() {
		clone.CreatedAt = time.Now().UTC()
	}
	clone.UpdatedAt = clone.CreatedAt
	r.items = append(r.items, &clone)
	*item = clone
	return nil
}

func (r *fakeRunRepo) FindByID(_ context.Context, ownerID, id int64) (*workflow.Run, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeRunRepo) ListByParent(_ context.Context, ownerID, parentRunID int64) ([]workflow.Run, error) {
	items := []workflow.Run{}
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ParentRunID != nil && *item.ParentRunID == parentRunID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeRunRepo) Update(_ context.Context, item *workflow.Run) error {
	for i := range r.items {
		if r.items[i].ID == item.ID {
			clone := *item
			r.items[i] = &clone
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

type fakeRunEventRepo struct {
	items []workflow.RunEvent
}

func (r *fakeRunEventRepo) Create(_ context.Context, item *workflow.RunEvent) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeRunEventRepo) ListByRun(context.Context, int64, int64) ([]workflow.RunEvent, error) {
	return r.items, nil
}

type fakeNodeLogRepo struct {
	items []workflow.NodeLog
}

func (fakeNodeLogRepo) Create(context.Context, *workflow.NodeLog) error { return nil }

func (fakeNodeLogRepo) Update(context.Context, *workflow.NodeLog) error { return nil }

func (r *fakeNodeLogRepo) ListByRun(context.Context, int64, int64) ([]workflow.NodeLog, error) {
	return append([]workflow.NodeLog(nil), r.items...), nil
}

func (r *fakeRunStepRepo) Create(_ context.Context, item *workflow.RunStep) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeRunStepRepo) ListByRun(context.Context, int64, int64) ([]workflow.RunStep, error) {
	return r.items, nil
}

func (r *fakeMemoryWriteLogRepo) Create(_ context.Context, item *memory.WriteLog) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeMemoryWriteLogRepo) ListByRun(context.Context, int64, int64) ([]memory.WriteLog, error) {
	return append([]memory.WriteLog(nil), r.items...), nil
}

func (r *fakeToolInvocationRepo) Create(_ context.Context, item *tool.Invocation) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeToolInvocationRepo) ListByRun(context.Context, int64, int64) ([]tool.Invocation, error) {
	return append([]tool.Invocation(nil), r.items...), nil
}

type fakeEvalRepo struct {
	datasets []*workflow.EvalDataset
	cases    []*workflow.EvalCase
	evalRuns []*workflow.EvalRun
	results  []*workflow.EvalResult
}

type fakeApprovalRepo struct {
	requests    []*workflow.ApprovalRequest
	checkpoints []*workflow.WorkflowCheckpoint
}

func (r *fakeApprovalRepo) CreateApprovalRequest(_ context.Context, item *workflow.ApprovalRequest) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(600 + len(r.requests))
	}
	r.requests = append(r.requests, &clone)
	*item = clone
	return nil
}

func (r *fakeApprovalRepo) FindApprovalRequestByID(_ context.Context, ownerID, id int64) (*workflow.ApprovalRequest, error) {
	for _, item := range r.requests {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeApprovalRepo) FindPendingApprovalByRun(_ context.Context, ownerID, runID int64) (*workflow.ApprovalRequest, error) {
	for _, item := range r.requests {
		if item.OwnerID == ownerID && item.RunID == runID && item.Status == workflow.ApprovalStatusPending {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeApprovalRepo) ListApprovalRequests(_ context.Context, ownerID int64, status string) ([]workflow.ApprovalRequest, error) {
	items := make([]workflow.ApprovalRequest, 0, len(r.requests))
	for _, item := range r.requests {
		if item.OwnerID == ownerID && (status == "" || item.Status == status) {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeApprovalRepo) UpdateApprovalRequest(_ context.Context, item *workflow.ApprovalRequest) error {
	for i := range r.requests {
		if r.requests[i].ID == item.ID {
			clone := *item
			r.requests[i] = &clone
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (r *fakeApprovalRepo) CreateCheckpoint(_ context.Context, item *workflow.WorkflowCheckpoint) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(700 + len(r.checkpoints))
	}
	r.checkpoints = append(r.checkpoints, &clone)
	*item = clone
	return nil
}

func (r *fakeApprovalRepo) FindLatestCheckpointByRun(_ context.Context, ownerID, runID int64) (*workflow.WorkflowCheckpoint, error) {
	for i := len(r.checkpoints) - 1; i >= 0; i-- {
		item := r.checkpoints[i]
		if item.OwnerID == ownerID && item.RunID == runID {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) CreateDataset(_ context.Context, item *workflow.EvalDataset) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(100 + len(r.datasets))
	}
	r.datasets = append(r.datasets, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) ListDatasetsByWorkflow(_ context.Context, ownerID, workflowID int64) ([]workflow.EvalDataset, error) {
	items := make([]workflow.EvalDataset, 0, len(r.datasets))
	for _, item := range r.datasets {
		if item.OwnerID == ownerID && item.WorkflowID == workflowID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeEvalRepo) FindDatasetByID(_ context.Context, ownerID, id int64) (*workflow.EvalDataset, error) {
	for _, item := range r.datasets {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) CreateCase(_ context.Context, item *workflow.EvalCase) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(200 + len(r.cases))
	}
	r.cases = append(r.cases, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) ListCasesByDataset(_ context.Context, ownerID, datasetID int64) ([]workflow.EvalCase, error) {
	items := make([]workflow.EvalCase, 0, len(r.cases))
	for _, item := range r.cases {
		if item.OwnerID == ownerID && item.DatasetID == datasetID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeEvalRepo) CreateEvalRun(_ context.Context, item *workflow.EvalRun) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(400 + len(r.evalRuns))
	}
	r.evalRuns = append(r.evalRuns, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) UpdateEvalRun(_ context.Context, item *workflow.EvalRun) error {
	for i := range r.evalRuns {
		if r.evalRuns[i].ID == item.ID {
			clone := *item
			r.evalRuns[i] = &clone
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) FindEvalRunByID(_ context.Context, ownerID, id int64) (*workflow.EvalRun, error) {
	for _, item := range r.evalRuns {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) ListEvalRunsByDataset(_ context.Context, ownerID, datasetID int64) ([]workflow.EvalRun, error) {
	items := make([]workflow.EvalRun, 0, len(r.evalRuns))
	for _, item := range r.evalRuns {
		if item.OwnerID == ownerID && item.DatasetID == datasetID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeEvalRepo) CreateEvalResult(_ context.Context, item *workflow.EvalResult) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(500 + len(r.results))
	}
	r.results = append(r.results, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) ListEvalResultsByRun(_ context.Context, ownerID, evalRunID int64) ([]workflow.EvalResult, error) {
	items := make([]workflow.EvalResult, 0, len(r.results))
	for _, item := range r.results {
		if item.OwnerID == ownerID && item.EvalRunID == evalRunID {
			items = append(items, *item)
		}
	}
	return items, nil
}

type fakeTeamRepo struct {
	teams   []*workflow.Team
	members []*workflow.TeamMember
}

func (r *fakeTeamRepo) CreateTeam(_ context.Context, item *workflow.Team) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(300 + len(r.teams))
	}
	r.teams = append(r.teams, &clone)
	*item = clone
	return nil
}

func (r *fakeTeamRepo) FindTeamByID(_ context.Context, ownerID, id int64) (*workflow.Team, error) {
	for _, item := range r.teams {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeTeamRepo) ListTeams(_ context.Context, ownerID int64) ([]workflow.Team, error) {
	items := make([]workflow.Team, 0, len(r.teams))
	for _, item := range r.teams {
		if item.OwnerID == ownerID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeTeamRepo) UpdateTeam(_ context.Context, item *workflow.Team) error {
	for i, existing := range r.teams {
		if existing.OwnerID == item.OwnerID && existing.ID == item.ID {
			clone := *item
			r.teams[i] = &clone
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (r *fakeTeamRepo) DeleteTeam(_ context.Context, ownerID, id int64) error { return nil }

func (r *fakeTeamRepo) AddMember(_ context.Context, item *workflow.TeamMember) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(400 + len(r.members))
	}
	r.members = append(r.members, &clone)
	*item = clone
	return nil
}

func (r *fakeTeamRepo) RemoveMember(_ context.Context, ownerID, teamID, workflowID int64) error {
	return nil
}

func (r *fakeTeamRepo) ListMembers(_ context.Context, ownerID, teamID int64) ([]workflow.TeamMember, error) {
	items := make([]workflow.TeamMember, 0, len(r.members))
	for _, item := range r.members {
		if item.OwnerID == ownerID && item.TeamID == teamID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeTeamRepo) ListMemberWorkflowIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error) {
	members, err := r.ListMembers(ctx, ownerID, teamID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, item := range members {
		ids = append(ids, item.WorkflowID)
	}
	return ids, nil
}

type fakeResumeLLM struct {
	responses []llm.ToolChatResponse
	requests  []llm.ToolChatRequest
}

func (c *fakeResumeLLM) Chat(context.Context, llm.ChatProviderConfig, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "unused"}, nil
}

func (c *fakeResumeLLM) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

func (c *fakeResumeLLM) ChatWithTools(_ context.Context, _ llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return &llm.ToolChatResponse{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return &resp, nil
}

type fakeResumeTool struct {
	name   string
	output string
	input  json.RawMessage
}

func (t *fakeResumeTool) Name() string { return t.name }

func (t *fakeResumeTool) Description() string { return "fake resume tool" }

func (t *fakeResumeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}}}`)
}

func (t *fakeResumeTool) Execute(_ context.Context, _ toolruntime.ToolRunContext, input json.RawMessage) (*toolruntime.ToolResult, error) {
	t.input = append(json.RawMessage(nil), input...)
	return &toolruntime.ToolResult{ContentText: t.output, ContentJSON: json.RawMessage(`{"ok":true}`)}, nil
}

func (t *fakeResumeTool) Metadata() toolruntime.ToolMetadata {
	return toolruntime.ToolMetadata{RiskLevel: toolruntime.RiskHigh, RequiresApproval: true, SideEffect: toolruntime.SideEffectExternalAction}
}

type fakeToolRegistry struct {
	tools []toolruntime.RuntimeTool
}

func (r fakeToolRegistry) LoadForAgent(context.Context, int64, []int64) ([]toolruntime.RuntimeTool, error) {
	return r.tools, nil
}

type fakeProviderRepo struct {
	items map[int64]*providerdomain.ModelProvider
}

func (r *fakeProviderRepo) Create(context.Context, *providerdomain.ModelProvider) error { return nil }

func (r *fakeProviderRepo) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}

func (r *fakeProviderRepo) FindByID(_ context.Context, ownerID, id int64) (*providerdomain.ModelProvider, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeProviderRepo) Update(context.Context, *providerdomain.ModelProvider) error { return nil }

func (r *fakeProviderRepo) SoftDelete(context.Context, int64, int64) error { return nil }

func jsonContains(raw json.RawMessage, text string) bool {
	return strings.Contains(string(raw), text)
}

func messagesContainToolResult(messages []llm.ChatMessage, toolCallID, content string) bool {
	for _, message := range messages {
		if message.Role == conversation.RoleTool && message.ToolCallID == toolCallID && strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}
