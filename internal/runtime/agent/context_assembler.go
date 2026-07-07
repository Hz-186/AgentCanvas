package agent

import (
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/contextcompress"
)

const defaultMaxInputChars = 24000
const defaultMaxInputTokens = defaultMaxInputChars / 4

type ContextAssembler struct {
	MaxChars       int
	MaxInputTokens int
}

func (a ContextAssembler) Build(req RunRequest) ([]llm.ChatMessage, ContextTrace) {
	maxChars := a.MaxChars
	if req.MaxInputChars > 0 {
		maxChars = req.MaxInputChars
	}
	maxTokens := a.MaxInputTokens
	if req.MaxInputTokens > 0 {
		maxTokens = req.MaxInputTokens
	}
	if maxTokens <= 0 && maxChars <= 0 {
		maxChars = defaultMaxInputChars
		maxTokens = defaultMaxInputTokens
	} else if maxTokens <= 0 {
		maxTokens = estimateContextTokens(strings.Repeat("x", maxChars))
	} else if maxChars <= 0 {
		maxChars = maxTokens * 4
	}
	trace := ContextTrace{
		MaxChars:       maxChars,
		MaxInputTokens: maxTokens,
		Strategy:       "token_budget:pinned_recent_summary_dedupe",
	}
	blocks := make([]ContextBlock, 0, len(req.ContextBlocks)+2)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		blocks = append(blocks, ContextBlock{
			Name:    "system",
			Role:    conversation.RoleSystem,
			Content: req.SystemPrompt,
			Pinned:  true,
		})
	}
	if modePrompt := modeInstruction(req); modePrompt != "" {
		blocks = append(blocks, ContextBlock{
			Name:    "agent_mode",
			Role:    conversation.RoleSystem,
			Content: modePrompt,
			Pinned:  true,
		})
	}
	if req.Plan != nil {
		if planContext := req.Plan.PlanContext(); strings.TrimSpace(planContext) != "" {
			blocks = append(blocks, ContextBlock{
				Name:    "execution_plan",
				Role:    conversation.RoleSystem,
				Content: planContext,
				Pinned:  true,
			})
		}
	}
	contextBlocks, preTrace := compressContextBlocks(req.ContextBlocks)
	for _, blockTrace := range preTrace {
		trace.Blocks = append(trace.Blocks, blockTrace)
		trace.SavedTokens += blockTrace.SavedTokens
		if blockTrace.Status == "compressed" {
			trace.Compressed = append(trace.Compressed, blockTrace.Name)
		} else if blockTrace.Status == "omitted" {
			trace.Omitted = append(trace.Omitted, blockTrace.Name)
		}
	}
	for _, block := range contextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		if block.Role == "" {
			block.Role = conversation.RoleUser
		}
		blocks = append(blocks, block)
	}
	blocks = append(blocks, ContextBlock{
		Name:    "task",
		Role:    conversation.RoleUser,
		Content: req.Task,
		Pinned:  true,
	})

	messages := make([]llm.ChatMessage, 0, len(blocks))
	used := 0
	usedTokens := 0
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if content == "" {
			continue
		}
		name := block.Name
		if name == "" {
			name = block.Role
		}
		originalChars := len(content)
		originalTokens := estimateContextTokens(content)
		blockTrace := ContextBlockTrace{
			Name:          name,
			Role:          block.Role,
			Pinned:        block.Pinned,
			OriginalChars: originalChars,
		}
		if used+len(content) > maxChars || usedTokens+originalTokens > maxTokens {
			if !block.Pinned {
				trace.Omitted = append(trace.Omitted, name)
				blockTrace.Status = "omitted"
				blockTrace.SavedTokens = originalTokens
				trace.SavedTokens += originalTokens
				trace.Blocks = append(trace.Blocks, blockTrace)
				continue
			}
			remaining := maxChars - used
			if tokenRemaining := (maxTokens - usedTokens) * 4; tokenRemaining > 0 && tokenRemaining < remaining {
				remaining = tokenRemaining
			}
			if remaining <= 0 {
				remaining = maxChars
			}
			if len(content) > remaining {
				content = content[:remaining]
				trace.Truncated = append(trace.Truncated, name)
				blockTrace.Status = "truncated"
				blockTrace.SavedTokens = originalTokens - estimateContextTokens(content)
				trace.SavedTokens += blockTrace.SavedTokens
			}
		}
		if blockTrace.Status == "" {
			blockTrace.Status = "included"
		}
		blockTrace.IncludedChars = len(content)
		blockTrace.EstimatedTokens = estimateContextTokens(content)
		messages = append(messages, llm.ChatMessage{Role: block.Role, Content: content})
		used += len(content)
		usedTokens += blockTrace.EstimatedTokens
		trace.Included = append(trace.Included, name)
		trace.EstimatedTokens += blockTrace.EstimatedTokens
		trace.TokenAudit.add(name, blockTrace.EstimatedTokens)
		trace.Blocks = append(trace.Blocks, blockTrace)
	}
	trace.UsedChars = used
	trace.UsedTokens = usedTokens
	trace.TokenAudit.Total = trace.EstimatedTokens
	return messages, trace
}

func compressContextBlocks(blocks []ContextBlock) ([]ContextBlock, []ContextBlockTrace) {
	blocks, historyTrace := summarizeHistoryBlocks(blocks, 4)
	blocks, dedupeTrace := dedupeRetrievalBlocks(blocks)
	return blocks, append(historyTrace, dedupeTrace...)
}

func summarizeHistoryBlocks(blocks []ContextBlock, keepRecent int) ([]ContextBlock, []ContextBlockTrace) {
	historyIndexes := make([]int, 0)
	for i, block := range blocks {
		if block.Pinned || tokenAuditCategory(block.Name) != "history" || strings.TrimSpace(block.Content) == "" {
			continue
		}
		historyIndexes = append(historyIndexes, i)
	}
	if len(historyIndexes) <= keepRecent {
		return blocks, nil
	}
	summarizedCount := len(historyIndexes) - keepRecent
	candidateIndexes := historyIndexes[:summarizedCount]
	items := make([]contextcompress.Item, 0, len(candidateIndexes))
	originalTokens := 0
	for turn, index := range candidateIndexes {
		content := strings.TrimSpace(blocks[index].Content)
		originalTokens += estimateContextTokens(content)
		items = append(items, contextcompress.Item{
			ID:      index,
			Content: content,
			Tokens:  estimateContextTokens(content),
			Turn:    turn + 1,
		})
	}
	selectionBudget := originalTokens / 3
	if selectionBudget < 1 {
		selectionBudget = 1
	}
	selection := contextcompress.Select(items, contextcompress.Options{
		Budget:             selectionBudget,
		Alpha:              0.08,
		DiversityLambda:    0.35,
		MinReferenceLength: 4,
		MaxNeighborScan:    96,
	})
	selected := make(map[int]bool, len(selection.Selected))
	for _, item := range selection.Selected {
		selected[item.Item.ID] = true
	}
	summarized := make(map[int]bool, summarizedCount)
	summaryParts := make([]string, 0, summarizedCount-len(selected))
	summarizedTokens := 0
	for _, index := range candidateIndexes {
		if selected[index] {
			continue
		}
		content := strings.TrimSpace(blocks[index].Content)
		summaryParts = append(summaryParts, "- "+firstContextLine(content, 160))
		summarized[index] = true
		summarizedTokens += estimateContextTokens(content)
	}
	if len(summaryParts) == 0 {
		return blocks, nil
	}
	summary := "Earlier conversation summary:\n" + strings.Join(summaryParts, "\n")
	result := make([]ContextBlock, 0, len(blocks)-summarizedCount+1)
	inserted := false
	for i, block := range blocks {
		if summarized[i] {
			if !inserted {
				result = append(result, ContextBlock{Name: "history_summary", Role: conversation.RoleSystem, Content: summary})
				inserted = true
			}
			continue
		}
		result = append(result, block)
	}
	savedTokens := summarizedTokens - estimateContextTokens(summary)
	if savedTokens < 0 {
		savedTokens = 0
	}
	return result, []ContextBlockTrace{{Name: "history_summary", Role: conversation.RoleSystem, OriginalChars: summarizedTokens * 4, IncludedChars: len(summary), EstimatedTokens: estimateContextTokens(summary), SavedTokens: savedTokens, Status: "compressed"}}
}

func dedupeRetrievalBlocks(blocks []ContextBlock) ([]ContextBlock, []ContextBlockTrace) {
	seen := map[string]bool{}
	result := make([]ContextBlock, 0, len(blocks))
	trace := make([]ContextBlockTrace, 0)
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if tokenAuditCategory(block.Name) != "retrieval" || content == "" {
			result = append(result, block)
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(content), " "))
		if seen[key] {
			name := block.Name
			if name == "" {
				name = block.Role
			}
			trace = append(trace, ContextBlockTrace{Name: name, Role: block.Role, OriginalChars: len(content), SavedTokens: estimateContextTokens(content), Status: "omitted"})
			continue
		}
		seen[key] = true
		result = append(result, block)
	}
	return result, trace
}

func firstContextLine(content string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "..."
}

func (a *TokenAudit) add(name string, tokens int) {
	if tokens <= 0 {
		return
	}
	switch tokenAuditCategory(name) {
	case "system":
		a.System += tokens
	case "rules_l1":
		a.RulesL1 += tokens
	case "rules_l2":
		a.RulesL2 += tokens
	case "rules_l3":
		a.RulesL3 += tokens
	case "tool_schema":
		a.ToolSchema += tokens
	case "history":
		a.History += tokens
	case "memory":
		a.Memory += tokens
	case "retrieval":
		a.Retrieval += tokens
	case "task":
		a.Task += tokens
	default:
		a.Profile += tokens
	}
}

func tokenAuditCategory(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "system" || name == "agent_mode":
		return "system"
	case strings.HasPrefix(name, "rule_l1") || strings.HasPrefix(name, "rules_l1"):
		return "rules_l1"
	case strings.HasPrefix(name, "rule_l2") || strings.HasPrefix(name, "rules_l2"):
		return "rules_l2"
	case strings.HasPrefix(name, "rule_l3") || strings.HasPrefix(name, "rules_l3"):
		return "rules_l3"
	case strings.Contains(name, "tool_schema"):
		return "tool_schema"
	case strings.Contains(name, "history") || strings.Contains(name, "conversation"):
		return "history"
	case strings.Contains(name, "memory"):
		return "memory"
	case strings.Contains(name, "retrieval") || strings.Contains(name, "knowledge") || strings.Contains(name, "citation"):
		return "retrieval"
	case name == "task":
		return "task"
	default:
		return "profile"
	}
}

func estimateContextTokens(content string) int {
	if content == "" {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, r := range content {
		if r < 128 {
			ascii++
		} else {
			nonASCII++
		}
	}
	estimate := nonASCII + ascii/4
	if estimate <= 0 {
		return 1
	}
	return estimate
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
