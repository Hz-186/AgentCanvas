package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/flow"
	providerdomain "agentcanvas/internal/domain/provider"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/runtime/engine"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/toolruntime"

	"gorm.io/gorm"
)

func TestCreateFlowVersionReusesEquivalentLatestVersion(t *testing.T) {
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{
		{ID: 10, OwnerID: 1, AgentID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`), IsDraft: true},
	}}
	service := newFlowVersionTestService(versions)

	// 位置和结构完全相同（仅 key 顺序不同），应复用已有版本
	created, err := service.CreateFlowVersion(context.Background(), 1, 20, CreateFlowVersionRequest{
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
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{
		{ID: 10, OwnerID: 1, AgentID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`), IsDraft: true},
	}}
	service := newFlowVersionTestService(versions)

	// 逻辑不变，仅节点位置改变，应创建新版本
	created, err := service.CreateFlowVersion(context.Background(), 1, 20, CreateFlowVersionRequest{
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
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{
		{ID: 10, OwnerID: 1, AgentID: 20, VersionNo: 1, DSLJSON: rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"}}}],"edges":[]}`), IsPublished: true},
	}}
	service := newFlowVersionTestService(versions)

	created, err := service.CreateFlowVersion(context.Background(), 1, 20, CreateFlowVersionRequest{
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
}

func TestGetAgentProfileCreatesDefaultProfile(t *testing.T) {
	profiles := &fakeProfileRepo{}
	service := &Service{
		agents:   &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "Researcher", Description: "Find facts", Status: agent.StatusActive}}},
		profiles: profiles,
	}
	profile, err := service.GetAgentProfile(context.Background(), 1, 20)
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
	profiles := &fakeProfileRepo{items: map[int64]*agent.Profile{
		20: {ID: 1, OwnerID: 1, AgentID: 20, Role: "Old", Goal: "Old goal", MaxIterations: 10, MaxExecutionTimeMS: 120000},
	}}
	service := &Service{
		agents:   &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "Agent", Status: agent.StatusActive}}},
		profiles: profiles,
	}
	tooMany := 99
	if _, err := service.UpdateAgentProfile(context.Background(), 1, 20, UpdateAgentProfileRequest{MaxIterations: &tooMany}); err == nil {
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
	updated, err := service.UpdateAgentProfile(context.Background(), 1, 20, UpdateAgentProfileRequest{
		Role:                     &role,
		Goal:                     &goal,
		DefaultToolPackIDs:       &defaultPacks,
		DefaultToolIDs:           &defaultTools,
		DefaultMCPServerIDs:      &defaultMCPServers,
		DefaultKnowledgeIDs:      &defaultKnowledge,
		DefaultKnowledgeTopK:     &topK,
		DefaultKnowledgeMode:     &mode,
		DefaultCallAgentIDs:      &defaultAgents,
		DefaultMaxAgentCallDepth: &maxDepth,
		OutputSchemaJSON:         &outputSchema,
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
	if got := updated.DefaultCallAgentIDsSlice(); len(got) != 1 || got[0] != 21 {
		t.Fatalf("unexpected default call agent ids: %+v", got)
	}
	if updated.DefaultKnowledgeTopK != 6 || updated.DefaultKnowledgeMode != "hybrid" || updated.DefaultMaxAgentCallDepth != 3 {
		t.Fatalf("unexpected profile defaults: %+v", updated)
	}
	if string(updated.OutputSchemaJSON) != `{"required":["answer"],"type":"object"}` {
		t.Fatalf("unexpected output schema: %s", string(updated.OutputSchemaJSON))
	}
	badTopK := 99
	if _, err := service.UpdateAgentProfile(context.Background(), 1, 20, UpdateAgentProfileRequest{DefaultKnowledgeTopK: &badTopK}); err == nil {
		t.Fatal("expected default_knowledge_top_k validation error")
	}
}

func TestCreateEvalDatasetPersistsDataset(t *testing.T) {
	evals := &fakeEvalRepo{}
	service := &Service{
		agents: &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "Agent", Status: agent.StatusActive}}},
		evals:  evals,
	}

	dataset, err := service.CreateEvalDataset(context.Background(), 1, 20, CreateEvalDatasetRequest{
		Name:        " Regression ",
		Description: "core cases",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dataset.ID == 0 || dataset.Name != "Regression" || dataset.AgentID != 20 || dataset.Status != agent.EvalDatasetStatusActive {
		t.Fatalf("unexpected dataset: %+v", dataset)
	}
	if len(evals.datasets) != 1 {
		t.Fatalf("expected one dataset, got %d", len(evals.datasets))
	}
}

func TestCreateEvalCaseNormalizesJSON(t *testing.T) {
	evals := &fakeEvalRepo{
		datasets: []*agent.EvalDataset{{ID: 30, OwnerID: 1, AgentID: 20, Name: "Regression", Status: agent.EvalDatasetStatusActive}},
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
		datasets: []*agent.EvalDataset{{ID: 30, OwnerID: 1, AgentID: 20, Name: "Regression", Status: agent.EvalDatasetStatusActive}},
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

func TestCallAgentRejectsIndirectCycle(t *testing.T) {
	events := &fakeRunEventRepo{}
	service := &Service{events: events}

	_, err := service.CallAgent(context.Background(), toolruntime.AgentCallRequest{
		OwnerID:       1,
		ParentRunID:   99,
		CallerAgentID: 21,
		CallerNodeID:  "call_agent",
		AgentID:       20,
		Input:         map[string]any{"query": "loop"},
		CallChain:     []int64{20, 21},
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
	if events.items[0].RunID != 99 || events.items[0].EventType != "agent_call_failed" || payload["blocked_reason"] != "agent_call_cycle_detected" {
		t.Fatalf("unexpected blocked event: %+v payload=%+v", events.items[0], payload)
	}
}

func TestListChildRunsReturnsRunsByParent(t *testing.T) {
	parentID := int64(10)
	otherParentID := int64(99)
	runs := &fakeRunRepo{items: []*agent.Run{
		{ID: parentID, OwnerID: 1, AgentID: 20, Status: agent.RunStatusSucceeded},
		{ID: 11, OwnerID: 1, AgentID: 21, ParentRunID: &parentID, Status: agent.RunStatusSucceeded},
		{ID: 12, OwnerID: 1, AgentID: 22, ParentRunID: &parentID, Status: agent.RunStatusFailed},
		{ID: 13, OwnerID: 1, AgentID: 23, ParentRunID: &otherParentID, Status: agent.RunStatusSucceeded},
		{ID: 14, OwnerID: 2, AgentID: 21, ParentRunID: &parentID, Status: agent.RunStatusSucceeded},
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

func TestServiceCreatesTeamAndMembers(t *testing.T) {
	teams := &fakeTeamRepo{}
	service := &Service{
		agents: &fakeAgentRepo{items: map[int64]*agent.Agent{
			20: {ID: 20, OwnerID: 1, Name: "Supervisor", Status: agent.StatusActive},
			21: {ID: 21, OwnerID: 1, Name: "Researcher", Status: agent.StatusActive},
		}},
		teams: teams,
	}

	team, err := service.CreateTeam(context.Background(), 1, CreateTeamRequest{
		Name:              "Research Team",
		SupervisorAgentID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if team.ID == 0 || team.HandoffStrategy != "supervisor" || team.MaxDepth != 3 {
		t.Fatalf("unexpected team: %+v", team)
	}
	handoffTeam, err := service.CreateTeam(context.Background(), 1, CreateTeamRequest{
		Name:              "Handoff Team",
		SupervisorAgentID: 20,
		HandoffStrategy:   "handoff",
		MaxDepth:          4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if handoffTeam.HandoffStrategy != "handoff" || handoffTeam.MaxDepth != 4 {
		t.Fatalf("unexpected handoff team: %+v", handoffTeam)
	}
	member, err := service.AddTeamMember(context.Background(), 1, team.ID, AddTeamMemberRequest{AgentID: 21, Role: "research"})
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
	if len(members) != 1 || members[0].AgentID != 21 {
		t.Fatalf("unexpected members: %+v", members)
	}
}

func TestRunEvalDatasetExecutesCasesAndScoresOutput(t *testing.T) {
	evals := &fakeEvalRepo{
		datasets: []*agent.EvalDataset{{ID: 30, OwnerID: 1, AgentID: 20, Name: "Regression", Status: agent.EvalDatasetStatusActive}},
		cases: []*agent.EvalCase{{
			ID:           200,
			OwnerID:      1,
			DatasetID:    30,
			Name:         "begin passthrough",
			InputJSON:    rawJSON(`{"final_answer":"hello world"}`),
			ExpectedJSON: rawJSON(`{"contains":"world"}`),
		}},
	}
	versions := &fakeFlowVersionRepo{items: []*agent.FlowVersion{{
		ID:          40,
		OwnerID:     1,
		AgentID:     20,
		VersionNo:   1,
		DSLJSON:     rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{}}],"edges":[]}`),
		IsPublished: true,
	}}}
	service := &Service{
		agents:     &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "Agent", Status: agent.StatusActive}}},
		versions:   versions,
		runs:       &fakeRunRepo{},
		events:     &fakeRunEventRepo{},
		nodeLogs:   &fakeNodeLogRepo{},
		evals:      evals,
		executor:   engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{})),
		validator:  flow.NewValidator(engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{}))),
		runCancels: newRunCancelRegistry(),
	}

	evalRun, results, err := service.RunEvalDataset(context.Background(), 1, 30, RunEvalDatasetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if evalRun.Status != agent.EvalRunStatusCompleted || evalRun.PassedCases != 1 || evalRun.SuccessRate != 1 {
		t.Fatalf("unexpected eval run: %+v", evalRun)
	}
	if len(results) != 1 || results[0].Status != "passed" || results[0].Score != 1 {
		t.Fatalf("unexpected eval results: %+v", results)
	}
	if len(evals.evalRuns) != 1 || len(evals.results) != 1 {
		t.Fatalf("expected persisted eval run/result, got %d/%d", len(evals.evalRuns), len(evals.results))
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
			"metadata":          map[string]any{"node_id": "agent"},
		},
	}
	err := service.persistRunCheckpointArtifacts(context.Background(), &agent.Run{ID: 7, OwnerID: 1, AgentID: 20}, output, agent.RunStatusWaitingHuman)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals.requests) != 1 || approvals.requests[0].ToolName != "send_email" || approvals.requests[0].Status != agent.ApprovalStatusPending {
		t.Fatalf("unexpected approval request: %+v", approvals.requests)
	}
	if len(approvals.checkpoints) != 1 || len(approvals.checkpoints[0].MessagesJSON) == 0 || approvals.checkpoints[0].ToolPolicyHash == "" {
		t.Fatalf("unexpected checkpoint: %+v", approvals.checkpoints)
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
	err := service.persistRunCheckpointArtifacts(context.Background(), &agent.Run{ID: 7, OwnerID: 1, AgentID: 20}, output, agent.RunStatusPaused)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals.requests) != 0 {
		t.Fatalf("paused checkpoint should not create approval: %+v", approvals.requests)
	}
	if len(approvals.checkpoints) != 1 || approvals.checkpoints[0].Status != agent.RunStatusPaused || len(approvals.checkpoints[0].MessagesJSON) == 0 {
		t.Fatalf("unexpected checkpoint: %+v", approvals.checkpoints)
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
	runs := &fakeRunRepo{items: []*agent.Run{{
		ID:             900,
		OwnerID:        1,
		AgentID:        20,
		FlowVersionID:  40,
		Status:         agent.RunStatusWaitingHuman,
		InputJSON:      rawJSON(`{"query":"please execute"}`),
		CallChainJSON:  rawJSON(`[20]`),
		StartedAt:      time.Now().UTC().Add(-time.Second),
		ConversationID: nil,
	}}}
	approvals := &fakeApprovalRepo{
		requests: []*agent.ApprovalRequest{{
			ID:           601,
			OwnerID:      1,
			AgentID:      20,
			RunID:        900,
			NodeID:       "agent_loop_1",
			ToolCallID:   "call_1",
			ToolName:     "dangerous_tool",
			RiskLevel:    "high",
			Status:       agent.ApprovalStatusApproved,
			DecisionNote: "approved",
		}},
		checkpoints: []*agent.AgentCheckpoint{{
			ID:                  701,
			OwnerID:             1,
			AgentID:             20,
			RunID:               900,
			NodeID:              "agent_loop_1",
			Status:              agent.RunStatusWaitingHuman,
			MessagesJSON:        mustJSON([]llm.ChatMessage{{Role: conversation.RoleUser, Content: "please execute"}, {Role: conversation.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "dangerous_tool", Arguments: json.RawMessage(`{"x":1}`)}}}}),
			PendingToolCallJSON: mustJSON(llm.ToolCall{ID: "call_1", Name: "dangerous_tool", Arguments: json.RawMessage(`{"x":1}`)}),
			MessagesSummary:     "assistant requested dangerous_tool",
		}},
	}
	service := &Service{
		agents: &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "Agent", Status: agent.StatusActive}}},
		profiles: &fakeProfileRepo{items: map[int64]*agent.Profile{20: {
			ID:                 1,
			OwnerID:            1,
			AgentID:            20,
			Role:               "Agent",
			Goal:               "Resume work",
			MaxIterations:      3,
			MaxExecutionTimeMS: 120000,
		}}},
		versions: &fakeFlowVersionRepo{items: []*agent.FlowVersion{{
			ID:        40,
			OwnerID:   1,
			AgentID:   20,
			VersionNo: 1,
			DSLJSON:   rawJSON(`{"schema_version":"v1","flow_id":"agent-20","nodes":[{"id":"agent_loop_1","type":"agent_loop","name":"Agent Loop","config":{"provider_id":1,"model":"gpt-test","task_template":"{{sys.query}}","tool_ids":[1],"max_iterations":3,"max_tool_calls":3,"max_execution_time_ms":120000,"require_approval_for_risk":["high"]}}],"edges":[]}`),
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
	if resumed.Status != agent.RunStatusSucceeded || !jsonContains(resumed.OutputJSON, "resumed final answer") {
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
		agents:    &fakeAgentRepo{items: map[int64]*agent.Agent{20: {ID: 20, OwnerID: 1, Name: "test", Status: agent.StatusActive}}},
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
	items map[int64]*agent.Agent
}

func (r *fakeAgentRepo) Create(context.Context, *agent.Agent) error { return nil }

func (r *fakeAgentRepo) ListByOwner(context.Context, int64) ([]agent.Agent, error) { return nil, nil }

func (r *fakeAgentRepo) FindByID(_ context.Context, ownerID, id int64) (*agent.Agent, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeAgentRepo) Update(context.Context, *agent.Agent) error { return nil }

func (r *fakeAgentRepo) SoftDelete(context.Context, int64, int64) error { return nil }

type fakeFlowVersionRepo struct {
	items       []*agent.FlowVersion
	createCalls int
	nextCalls   int
}

func (r *fakeFlowVersionRepo) Create(_ context.Context, item *agent.FlowVersion) error {
	r.createCalls++
	clone := *item
	clone.ID = int64(100 + r.createCalls)
	r.items = append(r.items, &clone)
	*item = clone
	return nil
}

func (r *fakeFlowVersionRepo) ListByAgent(_ context.Context, ownerID, agentID int64) ([]agent.FlowVersion, error) {
	items := make([]agent.FlowVersion, 0, len(r.items))
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeFlowVersionRepo) FindByID(_ context.Context, ownerID, id int64) (*agent.FlowVersion, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeFlowVersionRepo) FindCurrentByAgent(_ context.Context, ownerID, agentID int64) (*agent.FlowVersion, error) {
	var current *agent.FlowVersion
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && item.IsPublished && (current == nil || item.VersionNo > current.VersionNo) {
			current = item
		}
	}
	if current == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *current
	return &clone, nil
}

func (r *fakeFlowVersionRepo) FindLatestByAgent(_ context.Context, ownerID, agentID int64) (*agent.FlowVersion, error) {
	var latest *agent.FlowVersion
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && (latest == nil || item.VersionNo > latest.VersionNo) {
			latest = item
		}
	}
	if latest == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *latest
	return &clone, nil
}

func (r *fakeFlowVersionRepo) NextVersionNo(_ context.Context, ownerID, agentID int64) (int, error) {
	r.nextCalls++
	maxVersion := 0
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.AgentID == agentID && item.VersionNo > maxVersion {
			maxVersion = item.VersionNo
		}
	}
	return maxVersion + 1, nil
}

func (r *fakeFlowVersionRepo) Publish(context.Context, int64, int64, int64) error { return nil }

type fakeProfileRepo struct {
	items       map[int64]*agent.Profile
	updateCalls int
}

func (r *fakeProfileRepo) Create(_ context.Context, item *agent.Profile) error {
	if r.items == nil {
		r.items = map[int64]*agent.Profile{}
	}
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(len(r.items) + 1)
	}
	r.items[item.AgentID] = &clone
	*item = clone
	return nil
}

func (r *fakeProfileRepo) FindByAgent(_ context.Context, ownerID, agentID int64) (*agent.Profile, error) {
	item, ok := r.items[agentID]
	if !ok || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *item
	return &clone, nil
}

func (r *fakeProfileRepo) Update(_ context.Context, item *agent.Profile) error {
	r.updateCalls++
	if r.items == nil {
		r.items = map[int64]*agent.Profile{}
	}
	clone := *item
	r.items[item.AgentID] = &clone
	return nil
}

type fakeRunStepRepo struct {
	items []agent.RunStep
}

type fakeRunRepo struct {
	items []*agent.Run
}

func (r *fakeRunRepo) Create(_ context.Context, item *agent.Run) error {
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

func (r *fakeRunRepo) FindByID(_ context.Context, ownerID, id int64) (*agent.Run, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeRunRepo) ListByParent(_ context.Context, ownerID, parentRunID int64) ([]agent.Run, error) {
	items := []agent.Run{}
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ParentRunID != nil && *item.ParentRunID == parentRunID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeRunRepo) Update(_ context.Context, item *agent.Run) error {
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
	items []agent.RunEvent
}

func (r *fakeRunEventRepo) Create(_ context.Context, item *agent.RunEvent) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeRunEventRepo) ListByRun(context.Context, int64, int64) ([]agent.RunEvent, error) {
	return r.items, nil
}

type fakeNodeLogRepo struct{}

func (fakeNodeLogRepo) Create(context.Context, *agent.NodeLog) error { return nil }

func (fakeNodeLogRepo) Update(context.Context, *agent.NodeLog) error { return nil }

func (fakeNodeLogRepo) ListByRun(context.Context, int64, int64) ([]agent.NodeLog, error) {
	return nil, nil
}

func (r *fakeRunStepRepo) Create(_ context.Context, item *agent.RunStep) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeRunStepRepo) ListByRun(context.Context, int64, int64) ([]agent.RunStep, error) {
	return r.items, nil
}

type fakeEvalRepo struct {
	datasets []*agent.EvalDataset
	cases    []*agent.EvalCase
	evalRuns []*agent.EvalRun
	results  []*agent.EvalResult
}

type fakeApprovalRepo struct {
	requests    []*agent.ApprovalRequest
	checkpoints []*agent.AgentCheckpoint
}

func (r *fakeApprovalRepo) CreateApprovalRequest(_ context.Context, item *agent.ApprovalRequest) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(600 + len(r.requests))
	}
	r.requests = append(r.requests, &clone)
	*item = clone
	return nil
}

func (r *fakeApprovalRepo) FindApprovalRequestByID(_ context.Context, ownerID, id int64) (*agent.ApprovalRequest, error) {
	for _, item := range r.requests {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeApprovalRepo) FindPendingApprovalByRun(_ context.Context, ownerID, runID int64) (*agent.ApprovalRequest, error) {
	for _, item := range r.requests {
		if item.OwnerID == ownerID && item.RunID == runID && item.Status == agent.ApprovalStatusPending {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeApprovalRepo) ListApprovalRequests(_ context.Context, ownerID int64, status string) ([]agent.ApprovalRequest, error) {
	items := make([]agent.ApprovalRequest, 0, len(r.requests))
	for _, item := range r.requests {
		if item.OwnerID == ownerID && (status == "" || item.Status == status) {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeApprovalRepo) UpdateApprovalRequest(_ context.Context, item *agent.ApprovalRequest) error {
	for i := range r.requests {
		if r.requests[i].ID == item.ID {
			clone := *item
			r.requests[i] = &clone
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (r *fakeApprovalRepo) CreateCheckpoint(_ context.Context, item *agent.AgentCheckpoint) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(700 + len(r.checkpoints))
	}
	r.checkpoints = append(r.checkpoints, &clone)
	*item = clone
	return nil
}

func (r *fakeApprovalRepo) FindLatestCheckpointByRun(_ context.Context, ownerID, runID int64) (*agent.AgentCheckpoint, error) {
	for i := len(r.checkpoints) - 1; i >= 0; i-- {
		item := r.checkpoints[i]
		if item.OwnerID == ownerID && item.RunID == runID {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) CreateDataset(_ context.Context, item *agent.EvalDataset) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(100 + len(r.datasets))
	}
	r.datasets = append(r.datasets, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) ListDatasetsByAgent(_ context.Context, ownerID, agentID int64) ([]agent.EvalDataset, error) {
	items := make([]agent.EvalDataset, 0, len(r.datasets))
	for _, item := range r.datasets {
		if item.OwnerID == ownerID && item.AgentID == agentID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeEvalRepo) FindDatasetByID(_ context.Context, ownerID, id int64) (*agent.EvalDataset, error) {
	for _, item := range r.datasets {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) CreateCase(_ context.Context, item *agent.EvalCase) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(200 + len(r.cases))
	}
	r.cases = append(r.cases, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) ListCasesByDataset(_ context.Context, ownerID, datasetID int64) ([]agent.EvalCase, error) {
	items := make([]agent.EvalCase, 0, len(r.cases))
	for _, item := range r.cases {
		if item.OwnerID == ownerID && item.DatasetID == datasetID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeEvalRepo) CreateEvalRun(_ context.Context, item *agent.EvalRun) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(400 + len(r.evalRuns))
	}
	r.evalRuns = append(r.evalRuns, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) UpdateEvalRun(_ context.Context, item *agent.EvalRun) error {
	for i := range r.evalRuns {
		if r.evalRuns[i].ID == item.ID {
			clone := *item
			r.evalRuns[i] = &clone
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) FindEvalRunByID(_ context.Context, ownerID, id int64) (*agent.EvalRun, error) {
	for _, item := range r.evalRuns {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeEvalRepo) ListEvalRunsByDataset(_ context.Context, ownerID, datasetID int64) ([]agent.EvalRun, error) {
	items := make([]agent.EvalRun, 0, len(r.evalRuns))
	for _, item := range r.evalRuns {
		if item.OwnerID == ownerID && item.DatasetID == datasetID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeEvalRepo) CreateEvalResult(_ context.Context, item *agent.EvalResult) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(500 + len(r.results))
	}
	r.results = append(r.results, &clone)
	*item = clone
	return nil
}

func (r *fakeEvalRepo) ListEvalResultsByRun(_ context.Context, ownerID, evalRunID int64) ([]agent.EvalResult, error) {
	items := make([]agent.EvalResult, 0, len(r.results))
	for _, item := range r.results {
		if item.OwnerID == ownerID && item.EvalRunID == evalRunID {
			items = append(items, *item)
		}
	}
	return items, nil
}

type fakeTeamRepo struct {
	teams   []*agent.Team
	members []*agent.TeamMember
}

func (r *fakeTeamRepo) CreateTeam(_ context.Context, item *agent.Team) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(300 + len(r.teams))
	}
	r.teams = append(r.teams, &clone)
	*item = clone
	return nil
}

func (r *fakeTeamRepo) FindTeamByID(_ context.Context, ownerID, id int64) (*agent.Team, error) {
	for _, item := range r.teams {
		if item.OwnerID == ownerID && item.ID == id {
			clone := *item
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeTeamRepo) ListTeams(_ context.Context, ownerID int64) ([]agent.Team, error) {
	items := make([]agent.Team, 0, len(r.teams))
	for _, item := range r.teams {
		if item.OwnerID == ownerID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeTeamRepo) UpdateTeam(_ context.Context, item *agent.Team) error {
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

func (r *fakeTeamRepo) AddMember(_ context.Context, item *agent.TeamMember) error {
	clone := *item
	if clone.ID == 0 {
		clone.ID = int64(400 + len(r.members))
	}
	r.members = append(r.members, &clone)
	*item = clone
	return nil
}

func (r *fakeTeamRepo) RemoveMember(_ context.Context, ownerID, teamID, agentID int64) error {
	return nil
}

func (r *fakeTeamRepo) ListMembers(_ context.Context, ownerID, teamID int64) ([]agent.TeamMember, error) {
	items := make([]agent.TeamMember, 0, len(r.members))
	for _, item := range r.members {
		if item.OwnerID == ownerID && item.TeamID == teamID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *fakeTeamRepo) ListMemberAgentIDs(ctx context.Context, ownerID, teamID int64) ([]int64, error) {
	members, err := r.ListMembers(ctx, ownerID, teamID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, item := range members {
		ids = append(ids, item.AgentID)
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
