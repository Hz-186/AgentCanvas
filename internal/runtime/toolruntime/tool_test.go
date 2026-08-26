package toolruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/goal"
	runtimeevent "agentcanvas/internal/runtime/event"
)

func TestEffectiveLimit(t *testing.T) {
	for _, test := range []struct{ value, ceiling, want int }{{0, 30, 30}, {60, 30, 30}, {10, 30, 10}, {10, 0, 10}} {
		if got := EffectiveLimit(test.value, test.ceiling); got != test.want {
			t.Fatalf("EffectiveLimit(%d, %d) = %d, want %d", test.value, test.ceiling, got, test.want)
		}
	}
}

func TestValidateAllowedHostsNormalizesURLs(t *testing.T) {
	if err := ValidateAllowedHosts([]string{"HTTPS://API.EXAMPLE.COM:443/v1"}, []string{"api.example.com"}, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAllowedHosts([]string{"api.example.com"}, nil, true); err == nil {
		t.Fatal("deny-all policy accepted a tool host")
	}
}

func TestUpdatePlanToolMatchesCodexContract(t *testing.T) {
	const codexBaseline = "46aa019e805352c9d7fd9a740cbf7f8b9aeb162d"
	if codexBaseline == "" {
		t.Fatal("Codex baseline must stay explicit")
	}
	tool := UpdatePlanTool{}
	schema := string(tool.Parameters())
	for _, part := range []string{`"required":["plan"]`, `"additionalProperties":false`, `"pending"`, `"in_progress"`, `"completed"`} {
		if !strings.Contains(schema, part) {
			t.Fatalf("update_plan schema is missing %s: %s", part, schema)
		}
	}
	for _, input := range []string{
		`{"explanation":"x"}`,
		`{"plan":[],"unknown":true}`,
		`{"plan":[{"step":"x","status":"failed"}]}`,
		`{"plan":[{"step":"","status":"pending"}]}`,
	} {
		if _, err := tool.Execute(context.Background(), ToolRunContext{Mode: "default"}, json.RawMessage(input)); err == nil {
			t.Fatalf("invalid input was accepted: %s", input)
		}
	}
	var eventType string
	var payload map[string]any
	conversationID := int64(7)
	result, err := tool.Execute(context.Background(), ToolRunContext{
		Mode: "default", RunID: 9, ConversationID: &conversationID,
		EmitEvent: func(_ context.Context, kind string, value map[string]any) error {
			eventType, payload = kind, value
			return nil
		},
	}, json.RawMessage(`{"explanation":"working","plan":[{"step":"a","status":"in_progress"},{"step":"b","status":"in_progress"}]}`))
	if err != nil || result.ContentText != "Plan updated" || result.IsError {
		t.Fatalf("update_plan result = %+v, err=%v", result, err)
	}
	if eventType != runtimeevent.TodoUpdated || payload["conversation_id"] != int64(7) || len(payload["plan"].([]PlanItem)) != 2 {
		t.Fatalf("update_plan event = %q %#v", eventType, payload)
	}
}

func TestUpdatePlanToolReturnsExactPlanModeError(t *testing.T) {
	result, err := (UpdatePlanTool{}).Execute(context.Background(), ToolRunContext{Mode: "plan"}, json.RawMessage(`{"plan":[]}`))
	if err == nil || err.Error() != updatePlanModeError || result == nil || result.ContentText != updatePlanModeError || !result.IsError {
		t.Fatalf("Plan mode result=%+v err=%v", result, err)
	}
}

func TestRequestUserInputModeAvailabilityAndBlocking(t *testing.T) {
	tool := RequestUserInputTool{}
	question := `{"questions":[{"id":"scope","header":"范围","question":"选择范围","options":[{"label":"全部","description":"全部内容"}]}]}`
	if result, err := tool.Execute(context.Background(), ToolRunContext{Mode: "default"}, json.RawMessage(question)); err == nil || result == nil || !result.IsError {
		t.Fatalf("Default mode must reject request_user_input: result=%+v err=%v", result, err)
	}
	result, err := tool.Execute(context.Background(), ToolRunContext{Mode: "plan"}, json.RawMessage(question))
	if err != nil || result == nil || result.Approval == nil || len(result.Approval.Questions) != 1 {
		t.Fatalf("Plan mode request_user_input failed: result=%+v err=%v", result, err)
	}
	if !result.Approval.IsBlocking {
		t.Fatal("Plan mode request_user_input must be blocking")
	}
	result, err = tool.Execute(context.Background(), ToolRunContext{Mode: "default", DefaultModeRequestUserInput: true}, json.RawMessage(question))
	if err != nil || result == nil || result.Approval == nil || result.Approval.IsBlocking {
		t.Fatalf("feature-enabled Default request_user_input must be non-blocking: result=%+v err=%v", result, err)
	}
	if _, err := tool.Execute(context.Background(), ToolRunContext{Mode: "plan"}, json.RawMessage(question+` {}`)); err == nil {
		t.Fatal("trailing JSON must be rejected")
	}
	if result, err := tool.Execute(context.Background(), ToolRunContext{Mode: "plan", DelegationDepth: 1}, json.RawMessage(question)); err == nil || result == nil || !result.IsError {
		t.Fatalf("subagent request_user_input must be rejected: result=%+v err=%v", result, err)
	}
}

type memoryGoalRepository struct{ item *goal.ThreadGoal }

func (r *memoryGoalRepository) Get(context.Context, int64, int64) (*goal.ThreadGoal, error) {
	if r.item == nil {
		return nil, goal.ErrNotFound
	}
	return r.item, nil
}
func (r *memoryGoalRepository) Create(_ context.Context, item *goal.ThreadGoal) error {
	r.item = item
	return nil
}
func (r *memoryGoalRepository) Update(_ context.Context, item *goal.ThreadGoal, _ string) error {
	r.item = item
	return nil
}
func (r *memoryGoalRepository) Delete(context.Context, int64, int64) error { r.item = nil; return nil }
func (r *memoryGoalRepository) Account(context.Context, int64, int64, int64, int64, string) (*goal.ThreadGoal, error) {
	return r.item, nil
}
func (r *memoryGoalRepository) SetDeferral(context.Context, int64, int64, bool) error { return nil }
func (r *memoryGoalRepository) HasDeferral(context.Context, int64, int64) (bool, error) {
	return false, nil
}

func TestCreateGoalUsesConfiguredCeiling(t *testing.T) {
	ceiling := int64(100)
	repo := &memoryGoalRepository{}
	conversationID := int64(7)
	result, err := (CreateGoalTool{}).Execute(context.Background(), ToolRunContext{OwnerID: 1, ConversationID: &conversationID, GoalRepository: repo, GoalTokenBudgetCeiling: &ceiling}, json.RawMessage(`{"objective":"ship it"}`))
	if err != nil || result == nil || repo.item == nil || repo.item.TokenBudget == nil || *repo.item.TokenBudget != ceiling {
		t.Fatalf("default goal budget must use ceiling: result=%+v err=%v goal=%+v", result, err, repo.item)
	}
	if _, err := (CreateGoalTool{}).Execute(context.Background(), ToolRunContext{OwnerID: 1, ConversationID: &conversationID, GoalRepository: &memoryGoalRepository{}, GoalTokenBudgetCeiling: &ceiling}, json.RawMessage(`{"objective":"ship it","token_budget":101}`)); err == nil {
		t.Fatal("goal budget above configured ceiling must be rejected")
	}
}
