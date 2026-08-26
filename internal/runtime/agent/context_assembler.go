package agent

import (
	"sort"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
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
		RuleTrace:      req.RuleTrace,
		RuleHash:       req.RuleHash,
		Strategy:       "token_budget:pinned_recent_summary_dedupe",
	}
	blocks := make([]ContextBlock, 0, len(req.ContextBlocks)+2)
	systemPrompt := req.SystemPrompt
	if req.EnforceContextPrecedence && hasAdvisoryContext(req.ContextBlocks) && strings.TrimSpace(systemPrompt) != "" {
		systemPrompt += "\n\n" + contextPrecedenceInstruction
	}
	if strings.TrimSpace(systemPrompt) != "" {
		blocks = append(blocks, ContextBlock{
			Name:    "system",
			Role:    conversation.RoleSystem,
			Content: systemPrompt,
			Pinned:  true,
		})
	}
	if modePrompt := modeInstruction(req); modePrompt != "" {
		blocks = append(blocks, ContextBlock{
			Name:    "agent_mode",
			Role:    conversation.RoleDeveloper,
			Content: modePrompt,
			Pinned:  true,
		})
	}
	if req.EnforceContextPrecedence && hasAdvisoryContext(req.ContextBlocks) && strings.TrimSpace(systemPrompt) == "" {
		blocks = append(blocks, ContextBlock{
			Name: "context_precedence", Role: conversation.RoleSystem, Pinned: true,
			Content: contextPrecedenceInstruction,
		})
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
	sort.SliceStable(blocks, func(i, j int) bool {
		return blockSortPriority(blocks[i].Name) < blockSortPriority(blocks[j].Name)
	})
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
		if isMandatoryRuleBlock(name) {
			if counted := modelTokenCount(req, content).Tokens; counted > 0 {
				originalTokens = counted
			}
		}
		blockTrace := ContextBlockTrace{
			Name:          name,
			Role:          block.Role,
			Pinned:        block.Pinned,
			OriginalChars: originalChars,
		}
		if isMandatoryRuleBlock(name) {
			available := maxTokens - usedTokens
			if available < 0 {
				available = 0
			}
			if trace.MandatoryTokens == 0 || available < trace.MandatoryBudgetTokens {
				trace.MandatoryBudgetTokens = available
			}
			trace.MandatoryTokens += originalTokens
		}
		if used+len(content) > maxChars || usedTokens+originalTokens > maxTokens {
			if isMandatoryRuleBlock(name) {
				trace.CoreOverflow = true
				deficit := usedTokens + originalTokens - maxTokens
				if charOverflow := used + len(content) - maxChars; charOverflow > 0 {
					if charDeficit := estimateContextTokens(strings.Repeat("x", charOverflow)); charDeficit > deficit {
						deficit = charDeficit
					}
				}
				if deficit > trace.MandatoryDeficitTokens {
					trace.MandatoryDeficitTokens = deficit
				}
				blockTrace.Status = "core_overflow"
				trace.Blocks = append(trace.Blocks, blockTrace)
				continue
			}
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
				content = truncateRunes(content, remaining)
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
		if isMandatoryRuleBlock(name) {
			blockTrace.EstimatedTokens = originalTokens
		}
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

const contextPrecedenceInstruction = "Context precedence: the current user request and the latest conversation messages are authoritative. Conversation snapshots, working memory, retrieved memories, and search results are advisory and may be stale or contradictory. When they conflict, follow the latest explicit user instruction and state uncertainty instead of silently merging incompatible facts."

func hasAdvisoryContext(blocks []ContextBlock) bool {
	for _, block := range blocks {
		switch tokenAuditCategory(block.Name) {
		case "history", "working_memory", "memory", "retrieval", "reflection_memory":
			if strings.TrimSpace(block.Content) != "" {
				return true
			}
		}
	}
	return false
}

func isMandatoryRuleBlock(name string) bool {
	return tokenAuditCategory(name) == "rules_mandatory"
}

func blockSortPriority(name string) int {
	switch tokenAuditCategory(name) {
	case "system", "rules_mandatory", "rules_optional":
		return 0
	case "tool_schema":
		return 1
	case "memory":
		return 2
	case "history", "working_memory":
		return 3
	case "retrieval":
		return 4
	case "task":
		return 5
	default:
		return 3
	}
}

func truncateRunes(content string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	used := 0
	var b strings.Builder
	for _, r := range content {
		size := len(string(r))
		if used+size > maxBytes {
			break
		}
		b.WriteRune(r)
		used += size
	}
	return b.String()
}

func compressContextBlocks(blocks []ContextBlock) ([]ContextBlock, []ContextBlockTrace) {
	return dedupeContextBlocks(blocks)
}

// Context sources are intentionally layered: current conversation history is
// authoritative, while memory and working-memory blocks are advisory. Exact
// duplicates must be removed before token budgeting so a snapshot plus its
// live tail cannot occupy the window twice.
func dedupeContextBlocks(blocks []ContextBlock) ([]ContextBlock, []ContextBlockTrace) {
	seen := map[string]bool{}
	result := make([]ContextBlock, 0, len(blocks))
	trace := make([]ContextBlockTrace, 0)
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		category := tokenAuditCategory(block.Name)
		if content == "" || (category != "retrieval" && category != "history" && category != "working_memory" && category != "memory") {
			result = append(result, block)
			continue
		}
		key := category + "\x1f" + strings.ToLower(strings.Join(strings.Fields(content), " "))
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

func (a *TokenAudit) add(name string, tokens int) {
	if tokens <= 0 {
		return
	}
	switch tokenAuditCategory(name) {
	case "system":
		a.System += tokens
	case "rules_mandatory":
		a.RulesMandatory += tokens
	case "rules_optional":
		a.RulesOptional += tokens
	case "tool_schema":
		a.ToolSchema += tokens
	case "history":
		a.History += tokens
	case "working_memory":
		a.WorkingMemory += tokens
	case "memory":
		a.Memory += tokens
	case "reflection_memory":
		a.ReflectionMemory += tokens
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
	case strings.HasPrefix(name, "rule_mandatory") || strings.HasPrefix(name, "rules_mandatory"):
		return "rules_mandatory"
	case strings.HasPrefix(name, "rule_optional") || strings.HasPrefix(name, "rules_optional"):
		return "rules_optional"
	case strings.Contains(name, "tool_schema"):
		return "tool_schema"
	case strings.Contains(name, "history") || strings.Contains(name, "conversation"):
		return "history"
	case strings.Contains(name, "working_memory"):
		return "working_memory"
	case strings.Contains(name, "skills"):
		return "profile"
	case strings.Contains(name, "reflection_memory"):
		return "reflection_memory"
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
	switch strings.TrimSpace(req.Mode) {
	case "plan":
		return planModeDeveloperPrompt
	case "default":
		return "You are in Default mode. Execute the user's request, use tools when useful, verify changes, and return the result."
	case "":
		return ""
	default:
		return ""
	}
}

const planModeDeveloperPrompt = `# Plan Mode (Conversational)

You work in 3 phases, and you should chat your way to a great plan before finalizing it. A great plan is detailed in intent and implementation, and can be handed to another engineer or agent to implement immediately. It must be decision complete: the implementer should not need to make decisions.

## Mode rules (strict)

You are in Plan Mode until a developer message explicitly ends it. Plan Mode is not changed by user intent, tone, or imperative language. If a user asks for execution while still in Plan Mode, treat it as a request to plan the execution, not perform it.

## Plan Mode vs update_plan tool

Plan Mode is a collaboration mode that can involve requesting user input and eventually issuing a proposed_plan block. Separately, update_plan is a checklist/progress/TODOs tool; it does not enter or exit Plan Mode. Do not confuse update_plan with Plan Mode or try to use it while in Plan Mode. If you try to use update_plan in Plan Mode, it returns an error.

## Execution vs mutation in Plan Mode

You may explore and execute non-mutating actions that improve the plan. You must not perform mutating actions.

Allowed actions include reading or searching files, configs, schemas, types, manifests, and docs; static analysis and repo exploration; dry-run commands; and tests or builds that only write caches or build artifacts and do not edit tracked state.

Forbidden actions include editing or writing files; applying patches or migrations; running formatters or code generation that updates tracked files; and side-effectful commands whose purpose is to implement the plan. When in doubt, if an action would be described as doing the work rather than planning it, do not do it.

## PHASE 1 — Ground in the environment (explore first, ask second)

Begin by grounding yourself in the actual environment. Eliminate unknowns by discovering facts rather than asking. Resolve questions through inspection of relevant entrypoints, configs, schemas, types, and docs. Before asking any question, perform a targeted non-mutating exploration pass. Ask only when the answer cannot be derived from the environment.

## PHASE 2 — Intent chat (what they actually want)

Keep asking until you can clearly state the goal, success criteria, audience, in/out of scope, constraints, current state, and key preferences or tradeoffs. Bias toward questions over guessing when a high-impact ambiguity remains.

## PHASE 3 — Implementation chat (what/how we’ll build)

Once intent is stable, keep asking until the specification is decision complete: approach, interfaces and types, data flow, edge cases and failure modes, testing and acceptance criteria, rollout and monitoring, and migrations or compatibility constraints.

## Asking questions

Prefer request_user_input for important decisions. Offer only meaningful multiple-choice options. Ask only questions that materially change the specification, confirm an important assumption, or choose a meaningful tradeoff. Discoverable repository facts must be explored first.

## Finalization rule

Only output the final plan when it is decision complete. Wrap it in a proposed_plan block with the opening tag on its own line and the closing tag on its own line. Use Markdown inside the block. The plan must include a clear title, brief summary, important public API/interface/type changes, test cases and scenarios, and explicit assumptions and defaults. Prefer a compact structure with Summary, Key Changes or Implementation Changes, Test Plan, and Assumptions. Mention files only when needed to disambiguate. Do not ask whether to proceed in the final plan.

Only produce at most one proposed_plan block per turn. If the user requests revisions, produce a complete replacement plan. The plan block is the final artifact of Plan Mode; a developer message is required to end Plan Mode.

## Decision-complete checklist

Before finalizing, ensure every implementation decision is explicit: ownership of state, API wire shape, validation, persistence, ordering, error behavior, resume semantics, tests, and rollout. Keep the final plan concise but sufficient for direct implementation.`
