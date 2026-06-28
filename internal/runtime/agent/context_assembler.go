package agent

import (
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
)

const defaultMaxInputChars = 24000

type ContextAssembler struct {
	MaxChars int
}

func (a ContextAssembler) Build(req RunRequest) ([]llm.ChatMessage, ContextTrace) {
	maxChars := a.MaxChars
	if req.MaxInputChars > 0 {
		maxChars = req.MaxInputChars
	}
	if maxChars <= 0 {
		maxChars = defaultMaxInputChars
	}
	trace := ContextTrace{MaxChars: maxChars, Strategy: "priority_budget:pinned_then_recent"}
	blocks := make([]ContextBlock, 0, len(req.ContextBlocks)+2)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		blocks = append(blocks, ContextBlock{Name: "system", Role: conversation.RoleSystem, Content: req.SystemPrompt, Pinned: true})
	}
	if modePrompt := modeInstruction(req); modePrompt != "" {
		blocks = append(blocks, ContextBlock{Name: "agent_mode", Role: conversation.RoleSystem, Content: modePrompt, Pinned: true})
	}
	for _, block := range req.ContextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		if block.Role == "" {
			block.Role = conversation.RoleUser
		}
		blocks = append(blocks, block)
	}
	blocks = append(blocks, ContextBlock{Name: "task", Role: conversation.RoleUser, Content: req.Task, Pinned: true})

	messages := make([]llm.ChatMessage, 0, len(blocks))
	used := 0
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if content == "" {
			continue
		}
		name := block.Name
		if name == "" {
			name = block.Role
		}
		if used+len(content) > maxChars {
			if !block.Pinned {
				trace.Omitted = append(trace.Omitted, name)
				continue
			}
			remaining := maxChars - used
			if remaining <= 0 {
				remaining = maxChars
			}
			if len(content) > remaining {
				content = content[:remaining]
				trace.Truncated = append(trace.Truncated, name)
			}
		}
		messages = append(messages, llm.ChatMessage{Role: block.Role, Content: content})
		used += len(content)
		trace.Included = append(trace.Included, name)
	}
	trace.UsedChars = used
	return messages, trace
}

func modeInstruction(req RunRequest) string {
	mode := strings.TrimSpace(req.Mode)
	switch mode {
	case "", "react":
		if !req.ReflectionEnabled {
			return ""
		}
		return "Before producing the final answer, check whether the answer satisfies the task and whether tool errors require correction."
	case "plan_execute":
		return "Use a plan-and-execute approach: first outline a concise private execution plan in the response context, then call tools or answer step by step. If a step fails, revise the plan and continue within budget."
	case "reflect":
		return "Use reflection: after each tool result or draft answer, verify correctness, schema compliance, and missing evidence before finalizing."
	case "supervisor":
		return "Act as a supervisor Agent: delegate only to allowed specialist agents when useful, inspect their results, and keep responsibility for the final answer."
	default:
		return ""
	}
}
