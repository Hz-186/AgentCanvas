package rules

import (
	"strings"
)

func DefaultEnterpriseRegistry() *Registry {
	return NewRegistry(
		Rule{
			ID:       "safety.output.boundary",
			Name:     "Output Boundary",
			Level:    LevelL0Safety,
			Priority: 100,
			Content:  "Reject impossible claims, keep tool claims grounded in observed outputs, and do not fabricate execution results or external facts.",
			Activation: Activation{
				Always: true,
			},
		},
		Rule{
			ID:       "core.task.completion",
			Name:     "Task Completion",
			Level:    LevelL1Core,
			Priority: 90,
			Content:  "Answer the user's task directly, preserve critical constraints, and prefer concise execution over speculative discussion.",
			Activation: Activation{
				Always: true,
			},
		},
		Rule{
			ID:       "scenario.rag.citations",
			Name:     "RAG Citations",
			Level:    LevelL2Scenario,
			Priority: 70,
			Content:  "When retrieval content is present, cite or clearly attribute retrieved evidence and separate retrieved facts from reasoning.",
			Activation: Activation{
				TagAny:      []string{"retrieval", "knowledge", "rag"},
				ToolAny:     []string{"search_knowledge", "knowledge_search", "retrieve_chunks"},
				KeywordsAny: []string{"citation", "cite", "knowledge base", "retrieval"},
			},
		},
		Rule{
			ID:       "scenario.code.change_verification",
			Name:     "Code Verification",
			Level:    LevelL2Scenario,
			Priority: 80,
			Content:  "For code changes, keep edits minimal, verify behavior with focused tests or checks, and report what was validated versus what was not.",
			Activation: Activation{
				TagAny:      []string{"code", "repo", "git", "engineering"},
				ToolAny:     []string{"bash", "apply_patch"},
				KeywordsAny: []string{"test", "build", "compile", "refactor", "bug", "code"},
			},
		},
		Rule{
			ID:       "scenario.review.risk_first",
			Name:     "Review Risk First",
			Level:    LevelL2Scenario,
			Priority: 75,
			Content:  "For review tasks, lead with concrete findings, prioritize regressions and missing tests, and keep summaries secondary.",
			Activation: Activation{
				KeywordsAny: []string{"review", "code review", "审查", "评审"},
				TagAny:      []string{"review"},
			},
		},
		Rule{
			ID:       "tool.high_risk.approval",
			Name:     "High Risk Approval",
			Level:    LevelL3Tool,
			Priority: 85,
			Content:  "For high-risk or side-effecting tools, confirm policy and explain operational impact before relying on the tool result in the final answer.",
			Activation: Activation{
				RiskAny: []string{"high", "medium"},
				ToolAny: []string{"bash", "http_request", "call_workflow", "run_code"},
			},
		},
		Rule{
			ID:       "tool.plan_execute.checkpoints",
			Name:     "Plan Execute Checkpoints",
			Level:    LevelL3Tool,
			Priority: 65,
			Content:  "In plan-execute mode, keep steps explicit, revise the plan after failures, and avoid drifting into hidden execution state.",
			Activation: Activation{
				ModeAny: []string{"plan_execute", "reflect", "supervisor"},
			},
		},
		Rule{
			ID:       "ephemeral.long_context.compaction",
			Name:     "Long Context Compaction",
			Level:    LevelL4Ephemeral,
			Priority: 50,
			Content:  "When conversation context is long or repetitive, prefer summarized state over replaying every prior detail unless a rare signal would be lost.",
			Activation: Activation{
				KeywordsAny: []string{"summary", "compress", "long context", "32k", "窗口", "上下文"},
				TagAny:      []string{"long_context", "compression"},
			},
		},
	)
}

func registryForAgent(custom []Rule) *Registry {
	registry := DefaultEnterpriseRegistry()
	for _, rule := range custom {
		registry.Register(rule)
	}
	return registry
}

func ResolveForAgent(systemPrompt, task, mode, risk string, toolNames, tags []string, budget int, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	return ResolveWithRules(systemPrompt, task, mode, risk, toolNames, tags, budget, nil, audit, policy)
}

func ResolveWithRules(systemPrompt, task, mode, risk string, toolNames, tags []string, budget int, custom []Rule, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	return resolveForAgent(systemPrompt, task, mode, risk, toolNames, tags, budget, nil, custom, audit, policy)
}

// ResolveDynamicForAgent leaves permanent L0/L1 rules to the static context.
func ResolveDynamicForAgent(systemPrompt, task, mode, risk string, toolNames, tags []string, budget int, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	return ResolveDynamicWithRules(systemPrompt, task, mode, risk, toolNames, tags, budget, nil, audit, policy)
}

func ResolveDynamicWithRules(systemPrompt, task, mode, risk string, toolNames, tags []string, budget int, custom []Rule, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	return resolveForAgent(systemPrompt, task, mode, risk, toolNames, tags, budget, map[RuleLevel]bool{
		LevelL0Safety: true,
		LevelL1Core:   true,
	}, custom, audit, policy)
}

// ResolvePersistentForAgent returns only the permanent L0/L1 rules that are
// assembled once at run start. Dynamic rules are selected by RulePlanner.
func ResolvePersistentForAgent(systemPrompt, task, mode, risk string, toolNames, tags []string, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	return ResolvePersistentWithRules(systemPrompt, task, mode, risk, toolNames, tags, nil, audit, policy)
}

func ResolvePersistentWithRules(systemPrompt, task, mode, risk string, toolNames, tags []string, custom []Rule, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	return resolveForAgent(systemPrompt, task, mode, risk, toolNames, tags, 0, map[RuleLevel]bool{
		LevelL2Scenario:  true,
		LevelL3Tool:      true,
		LevelL4Ephemeral: true,
	}, custom, audit, policy)
}

func resolveForAgent(systemPrompt, task, mode, risk string, toolNames, tags []string, budget int, excluded map[RuleLevel]bool, custom []Rule, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	registry := registryForAgent(custom)
	ctx := LoadContext{
		Mode:          strings.TrimSpace(mode),
		RiskLevel:     strings.TrimSpace(risk),
		ToolNames:     append([]string(nil), toolNames...),
		Tags:          dedupeStrings(tags),
		Task:          strings.TrimSpace(task),
		Conversation:  strings.TrimSpace(systemPrompt),
		TokenBudget:   budget,
		LevelBudgets:  DefaultLevelBudgets(budget),
		ScoreCutoff:   110,
		MaxCandidates: 12,
		ExcludeLevels: excluded,
	}
	if audit != nil {
		return registry.LoadWithAudit(ctx, audit, policy)
	}
	return registry.Load(ctx)
}
