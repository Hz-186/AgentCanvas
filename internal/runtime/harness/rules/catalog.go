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

func ResolveForAgent(systemPrompt, task, mode, risk string, toolNames, tags []string, budget int, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	registry := DefaultEnterpriseRegistry()
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
	}
	if audit != nil {
		return registry.LoadWithAudit(ctx, audit, policy)
	}
	return registry.Load(ctx)
}
