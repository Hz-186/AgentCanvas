package agent

import (
	"context"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

func TestPlanCurrentStepAndCompletion(t *testing.T) {
	plan := Plan{
		Steps: []PlanStep{
			{Number: 1, Description: "search knowledge", Status: "pending"},
			{Number: 2, Description: "analyze results", Status: "pending"},
			{Number: 3, Description: "write answer", Status: "pending"},
		},
	}
	current := plan.CurrentStep()
	if current == nil || current.Number != 1 {
		t.Fatalf("expected step 1, got %+v", current)
	}
	plan.StepCompleted(1)
	current = plan.CurrentStep()
	if current == nil || current.Number != 2 {
		t.Fatalf("expected step 2, got %+v", current)
	}
	plan.StepCompleted(2)
	plan.StepCompleted(3)
	current = plan.CurrentStep()
	if current != nil {
		t.Fatalf("expected nil after all steps completed, got %+v", current)
	}
}

func TestPlanContextOutput(t *testing.T) {
	plan := Plan{
		Steps: []PlanStep{
			{Number: 1, Description: "step one", Status: "completed"},
			{Number: 2, Description: "step two", Status: "pending"},
			{Number: 3, Description: "step three", Status: "pending"},
		},
	}
	ctx := plan.PlanContext()
	if ctx == "" {
		t.Fatal("expected non-empty plan context")
	}
	if len(ctx) == 0 {
		t.Fatal("expected plan context output")
	}
}

func TestPlanEmptyContext(t *testing.T) {
	plan := Plan{}
	if ctx := plan.PlanContext(); ctx != "" {
		t.Fatalf("expected empty context for empty plan, got %q", ctx)
	}
}

func TestPlanCompletedSkipsNonPending(t *testing.T) {
	plan := Plan{
		Steps: []PlanStep{
			{Number: 1, Description: "done", Status: "completed"},
			{Number: 2, Description: "in progress", Status: "pending"},
		},
	}
	plan.StepCompleted(1)
	if plan.Steps[1].Status != "pending" {
		t.Fatalf("step 2 should still be pending, got %s", plan.Steps[1].Status)
	}
}

func TestPlannerExtractJSONContent(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{`{"steps":[]}`, `{"steps":[]}`},
		{"some text {\"steps\":[{\"number\":1}]} more text", `{"steps":[{"number":1}]}`},
		{"{\"steps\":[{\"number\":1}]}\n", `{"steps":[{"number":1}]}`},
	}
	for _, tc := range cases {
		result := extractJSONContent(tc.input)
		if result != tc.expected {
			t.Errorf("input %q: expected %q, got %q", tc.input, tc.expected, result)
		}
	}
}

func TestPlannerMaxStepsVal(t *testing.T) {
	p := Planner{MaxSteps: 0}
	if p.maxStepsVal() != 8 {
		t.Fatalf("expected default 8, got %d", p.maxStepsVal())
	}
	p.MaxSteps = 5
	if p.maxStepsVal() != 5 {
		t.Fatalf("expected 5, got %d", p.maxStepsVal())
	}
}

type fakePlannerLLM struct {
	response string
}

func (c *fakePlannerLLM) ChatWithTools(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	return &llm.ToolChatResponse{
		Message:  llm.ChatMessage{Role: conversation.RoleAssistant, Content: c.response},
		Usage:    llm.Usage{TotalTokens: 10},
	}, nil
}

func (c *fakePlannerLLM) Chat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, nil
}

func TestPlannerGeneratePlan(t *testing.T) {
	planJSON := `{
		"steps": [
			{"number": 1, "description": "search docs"},
			{"number": 2, "description": "analyze"},
			{"number": 3, "description": "write answer"}
		]
	}`
	client := &fakePlannerLLM{response: planJSON}
	planner := &Planner{LLM: client, MaxSteps: 5}
	plan, err := planner.GeneratePlan(context.Background(), llm.ChatProviderConfig{}, "test-model", "task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	for _, s := range plan.Steps {
		if s.Status != "pending" {
			t.Fatalf("expected all steps pending, got %+v", s)
		}
	}
}

func TestPlannerRequiresClient(t *testing.T) {
	planner := &Planner{LLM: nil}
	_, err := planner.GeneratePlan(context.Background(), llm.ChatProviderConfig{}, "model", "task", nil)
	if err == nil {
		t.Fatal("expected error without LLM client")
	}
}

func TestPlanFinished(t *testing.T) {
	plan := Plan{
		Steps: []PlanStep{
			{Number: 1, Description: "step one", Status: "completed"},
			{Number: 2, Description: "step two", Status: "completed"},
		},
		Finished: true,
	}
	if !plan.Finished {
		t.Fatal("expected plan to be finished")
	}
}
