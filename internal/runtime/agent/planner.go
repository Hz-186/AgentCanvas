package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

type PlanStep struct {
	// Number is stable across plan revisions.
	Number int `json:"number"`
	// Description is the step instruction for the execution model.
	Description string `json:"description"`
	// Status is plan state, not an execution trace event.
	Status string `json:"status"`
	// ToolName is a planner hint, not an enforced constraint.
	ToolName string `json:"tool_name,omitempty"`
}

type Plan struct {
	// Steps is the plan snapshot for the run.
	Steps []PlanStep `json:"steps"`
	// Finished is reserved for verified step completion; guided plans end unverified.
	Finished bool `json:"finished"`
	// Version increments after each successful revision.
	Version int `json:"version,omitempty"`
	// RevisionReason records the feedback that triggered the revision.
	RevisionReason string `json:"revision_reason,omitempty"`
	// ExecutionState distinguishes a guided plan ending from verified completion.
	ExecutionState string `json:"execution_state,omitempty"`
}

type Planner struct {
	LLM        llm.ToolCallingClient
	MaxSteps   int
	OnStep     StepEmitter
	ProviderID int64
	ModelName  string
}

func (p Planner) maxStepsVal() int {
	if p.MaxSteps <= 0 {
		return 8
	}
	return p.MaxSteps
}

func (p Planner) GeneratePlan(
	ctx context.Context, provider llm.ChatProviderConfig, model, task string, temperature *float64,
) (*Plan, error) {
	return p.GeneratePlanWithLessons(ctx, provider, model, task, "", temperature)
}

func (p Planner) GeneratePlanWithLessons(
	ctx context.Context, provider llm.ChatProviderConfig, model, task, lessons string, temperature *float64,
) (*Plan, error) {
	if p.LLM == nil {
		return nil, fmt.Errorf("planner requires a tool calling client")
	}
	// Planning is a tool-free LLM call that returns JSON only.
	prompt := fmt.Sprintf(`Create a concise execution plan with 3 to %d steps for: %s

Relevant lessons from prior runs (advisory; do not override current instructions):
%s

Return the plan as a JSON object with a "steps" array. Each step has "number", "description", and optionally "tool_name".
Do NOT use tool calls in this response — output only the JSON plan.`, p.maxStepsVal(), task, lessons)

	resp, err := p.LLM.ChatWithTools(ctx, provider, llm.ToolChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: conversation.RoleUser, Content: prompt},
		},
		Tools:       nil,
		Temperature: temperature,
	})
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(resp.Message.Content)
	content = extractJSONContent(content)
	var plan Plan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan: %w", err)
	}
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("plan is empty")
	}
	for i := range plan.Steps {
		// Initial plans always start with pending steps.
		plan.Steps[i].Status = "pending"
	}
	if plan.Version <= 0 {
		plan.Version = 1
	}
	plan.ExecutionState = "active"
	return &plan, nil
}

func (p Planner) RevisePlan(ctx context.Context, provider llm.ChatProviderConfig, model, task string, current *Plan, feedback string, temperature *float64) (*Plan, llm.Usage, error) {
	if p.LLM == nil || current == nil {
		return nil, llm.Usage{}, fmt.Errorf("planner and current plan are required")
	}
	// Keep the run-level task stable across revisions.
	currentJSON, _ := json.Marshal(current)
	prompt := fmt.Sprintf(`Revise only the unfinished portion of this execution plan after a verified failure.
Do not repeat completed side-effecting actions. Preserve completed steps and return the full plan as JSON.

Task: %s
Current plan: %s
Reflection feedback: %s

Return only {"steps":[{"number":1,"description":"...","status":"completed|pending","tool_name":""}]}.`, task, currentJSON, feedback)
	resp, err := p.LLM.ChatWithTools(ctx, provider, llm.ToolChatRequest{Model: model, Messages: []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}}, Tools: nil, Temperature: temperature})
	if err != nil {
		return nil, llm.Usage{}, err
	}
	content := extractJSONContent(strings.TrimSpace(resp.Message.Content))
	var revised Plan
	if err := json.Unmarshal([]byte(content), &revised); err != nil || len(revised.Steps) == 0 {
		if err == nil {
			err = fmt.Errorf("revised plan is empty")
		}
		return nil, resp.Usage, err
	}
	revised.Steps = preserveCompletedPlanSteps(current.Steps, revised.Steps)
	if len(revised.Steps) == 0 {
		return nil, resp.Usage, fmt.Errorf("revised plan has no executable steps")
	}
	revised.Version = current.Version + 1
	revised.RevisionReason = feedback
	revised.ExecutionState = "active"
	return &revised, resp.Usage, nil
}

// preserveCompletedPlanSteps treats planner output as an untrusted proposal.
// A revision may change unfinished work, but it must never replay, rewrite, or
// silently drop a step whose side effects have already completed.
func preserveCompletedPlanSteps(current, proposed []PlanStep) []PlanStep {
	completed := make(map[int]PlanStep, len(current))
	for _, step := range current {
		if step.Status == "completed" {
			step.Status = "completed"
			completed[step.Number] = step
		}
	}

	result := make([]PlanStep, 0, len(proposed)+len(completed))
	seen := make(map[int]bool, len(proposed)+len(completed))
	for _, step := range proposed {
		if step.Number <= 0 || seen[step.Number] {
			continue
		}
		seen[step.Number] = true
		if preserved, ok := completed[step.Number]; ok {
			result = append(result, preserved)
			delete(completed, step.Number)
			continue
		}
		// The model cannot declare previously unfinished work completed.
		step.Status = "pending"
		result = append(result, step)
	}
	for _, step := range current {
		preserved, ok := completed[step.Number]
		if !ok || seen[step.Number] {
			continue
		}
		seen[step.Number] = true
		result = append(result, preserved)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result
}

func (p Plan) CurrentStep() *PlanStep {
	for i := range p.Steps {
		if p.Steps[i].Status == "pending" {
			return &p.Steps[i]
		}
	}
	return nil
}

func (p Plan) PlanContext() string {
	if len(p.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nExecution Plan:\n")
	if p.Version > 0 {
		b.WriteString(fmt.Sprintf("Version: %d\n", p.Version))
	}
	if p.RevisionReason != "" {
		b.WriteString("Revision reason: " + p.RevisionReason + "\n")
	}
	for _, s := range p.Steps {
		status := s.Status
		if status == "" {
			status = "pending"
		}
		b.WriteString(fmt.Sprintf("  Step %d: %s [%s]\n", s.Number, s.Description, status))
	}
	current := p.CurrentStep()
	if current != nil {
		b.WriteString(fmt.Sprintf("\nNow executing Step %d: %s\n", current.Number, current.Description))
	}
	return b.String()
}

func extractJSONContent(content string) string {
	content = strings.TrimSpace(content)
	if idx := strings.Index(content, "{"); idx >= 0 {
		content = content[idx:]
	}
	if idx := strings.LastIndex(content, "}"); idx >= 0 {
		content = strings.TrimSpace(content[:idx+1])
	}
	return content
}

func (p Plan) StepCompleted(stepNumber int) {
	for i := range p.Steps {
		if p.Steps[i].Number == stepNumber && p.Steps[i].Status == "pending" {
			p.Steps[i].Status = "completed"
			break
		}
	}
}

func (p *Plan) Finish() {
	if p == nil {
		return
	}
	// Completion follows the model's final answer, not per-step verification.
	for i := range p.Steps {
		if p.Steps[i].Status == "" || p.Steps[i].Status == "pending" {
			p.Steps[i].Status = "completed"
		}
	}
	p.Finished = true
}

func (p *Plan) EndUnverified() {
	if p == nil {
		return
	}
	p.ExecutionState = "ended_unverified"
	p.Finished = false
}

func (p *Planner) appendStep(result *RunResult, step RunStep) RunStep {
	step.Index = len(result.Steps) + 1
	step.CreatedAt = time.Now().UTC()
	if len(step.ArgumentsJSON) == 0 {
		step.ArgumentsJSON = nil
	}
	if len(step.OutputJSON) == 0 {
		step.OutputJSON = nil
	}
	step.ProviderID = p.ProviderID
	step.Model = p.ModelName
	result.Steps = append(result.Steps, step)
	return step
}
