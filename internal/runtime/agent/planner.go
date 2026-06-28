package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

type PlanStep struct {
	Number      int    `json:"number"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ToolName    string `json:"tool_name,omitempty"`
}

type Plan struct {
	Steps    []PlanStep `json:"steps"`
	Finished bool       `json:"finished"`
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

func (p Planner) GeneratePlan(ctx context.Context, provider llm.ChatProviderConfig, model, task string, temperature *float64) (*Plan, error) {
	if p.LLM == nil {
		return nil, fmt.Errorf("planner requires a tool calling client")
	}
	prompt := fmt.Sprintf(`Create a concise execution plan with 3 to %d steps for: %s

Return the plan as a JSON object with a "steps" array. Each step has "number", "description", and optionally "tool_name".
Do NOT use tool calls in this response — output only the JSON plan.`, p.maxStepsVal(), task)

	resp, err := p.LLM.ChatWithTools(ctx, provider, llm.ToolChatRequest{
		Model:       model,
		Messages:    []llm.ChatMessage{{Role: conversation.RoleUser, Content: prompt}},
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
		plan.Steps[i].Status = "pending"
	}
	return &plan, nil
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
