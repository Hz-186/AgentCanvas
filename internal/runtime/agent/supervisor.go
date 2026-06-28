package agent

import (
	"encoding/json"
	"fmt"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

type SupervisorRuntime struct {
	LLM            llm.ToolCallingClient
	WorkflowCaller toolruntime.WorkflowCaller
}

type SupervisorPlan struct {
	Delegations []SupervisorDelegation `json:"delegations"`
}

type SupervisorDelegation struct {
	WorkflowID int64  `json:"workflow_id"`
	Task       string `json:"task"`
	AgentRole  string `json:"agent_role,omitempty"`
}

func (s *SupervisorRuntime) BuildSupervisorPrompt(teamMembers []TeamMemberInfo) string {
	prompt := "You are a supervisor workflow. You have the following team members:\n\n"
	for _, m := range teamMembers {
		prompt += fmt.Sprintf("- Agent %d (%s): %s\n", m.WorkflowID, m.Role, m.Description)
	}
	prompt += "\nYou can delegate tasks to team members using the call_workflow tool. "
	prompt += "Always delegate to the most appropriate team member for each task. "
	prompt += "After receiving results from team members, review and synthesize the final answer. "
	prompt += "You are responsible for the final response quality.\n"
	return prompt
}

type TeamMemberInfo struct {
	WorkflowID  int64  `json:"workflow_id"`
	Role        string `json:"role"`
	Description string `json:"description"`
}

func CheckCallChain(callChain []int64, targetWorkflowID int64, maxDepth int, currentDepth int) error {
	for _, id := range callChain {
		if id == targetWorkflowID {
			return fmt.Errorf("circular delegation detected: agent %d is already in the call chain", targetWorkflowID)
		}
	}
	if maxDepth > 0 && currentDepth >= maxDepth {
		return fmt.Errorf("max workflow call depth exceeded: current=%d max=%d", currentDepth, maxDepth)
	}
	return nil
}

func CompactToolOutput(content string, maxBytes int) string {
	if maxBytes <= 0 {
		return content
	}
	if len(content) <= maxBytes {
		return content
	}
	return content[:maxBytes] + "...[compressed]"
}

func RedactSensitiveFields(raw json.RawMessage, fields []string) json.RawMessage {
	if len(fields) == 0 || len(raw) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	for _, field := range fields {
		if _, ok := m[field]; ok {
			m[field] = "[REDACTED]"
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return json.RawMessage(out)
}
