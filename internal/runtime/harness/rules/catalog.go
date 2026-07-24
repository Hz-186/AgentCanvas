package rules

// DefaultPlatformMandatoryRules are platform constraints injected for every run.
func DefaultPlatformMandatoryRules() []Rule {
	return []Rule{
		{
			ID:       "safety.output.boundary",
			Name:     "Output Boundary",
			Strength: RuleMandatory,
			Priority: 100,
			Content:  "Reject impossible claims, keep tool claims grounded in observed outputs, and do not fabricate execution results or external facts.",
		},
		{
			ID:       "core.task.completion",
			Name:     "Task Completion",
			Strength: RuleMandatory,
			Priority: 90,
			Content:  "Answer the user's task directly, preserve critical constraints, and prefer concise execution over speculative discussion.",
		},
	}
}

// DefaultFallbackOptionalRules are used only when no versioned RuleSet is active.
func DefaultFallbackOptionalRules() []Rule {
	return []Rule{
		{
			ID:       "scenario.rag.citations",
			Name:     "RAG Citations",
			Strength: RuleOptional,
			Priority: 70,
			Content:  "When retrieval content is present, cite or clearly attribute retrieved evidence and separate retrieved facts from reasoning.",
			Activation: Activation{
				TagAny:      []string{"retrieval", "knowledge", "rag"},
				ToolAny:     []string{"search_knowledge", "knowledge_search", "retrieve_chunks"},
				KeywordsAny: []string{"citation", "cite", "knowledge base", "retrieval"},
			},
		},
		{
			ID:       "scenario.code.change_verification",
			Name:     "Code Verification",
			Strength: RuleOptional,
			Priority: 80,
			Content:  "For code changes, keep edits minimal, verify behavior with focused tests or checks, and report what was validated versus what was not.",
			Activation: Activation{
				TagAny:      []string{"code", "repo", "git", "engineering"},
				ToolAny:     []string{"bash", "apply_patch"},
				KeywordsAny: []string{"test", "build", "compile", "refactor", "bug", "code"},
			},
		},
		{
			ID:       "scenario.review.risk_first",
			Name:     "Review Risk First",
			Strength: RuleOptional,
			Priority: 75,
			Content:  "For review tasks, lead with concrete findings, prioritize regressions and missing tests, and keep summaries secondary.",
			Activation: Activation{
				KeywordsAny: []string{"review", "code review", "审查", "评审"},
				TagAny:      []string{"review"},
			},
		},
		{
			ID:       "tool.high_risk.approval",
			Name:     "High Risk Approval",
			Strength: RuleOptional,
			Priority: 85,
			Content:  "For high-risk or side-effecting tools, confirm policy and explain operational impact before relying on the tool result in the final answer.",
			Activation: Activation{
				RiskAny: []string{"high", "medium"},
				ToolAny: []string{"bash", "http_request", "call_workflow", "run_code"},
				TagAll:  []string{"tool_used"},
			},
		},
		{
			ID:       "tool.plan_execute.checkpoints",
			Name:     "Plan Execute Checkpoints",
			Strength: RuleOptional,
			Priority: 65,
			Content:  "In plan-execute mode, keep steps explicit, revise the plan after failures, and avoid drifting into hidden execution state.",
			Activation: Activation{
				ModeAny: []string{"plan_execute", "reflect", "supervisor"},
				TagAll:  []string{"tool_used"},
			},
		},
		{
			ID:       "ephemeral.long_context.compaction",
			Name:     "Long Context Compaction",
			Strength: RuleOptional,
			Priority: 50,
			Content:  "When conversation context is long or repetitive, prefer summarized state over replaying every prior detail unless a rare signal would be lost.",
			Activation: Activation{
				KeywordsAny: []string{"summary", "compress", "long context", "32k", "窗口", "上下文"},
				TagAny:      []string{"long_context", "compression"},
			},
		},
	}
}

func RuntimeRules(custom []Rule, includeFallback bool) ([]Rule, error) {
	validated, err := ValidateRules(custom)
	if err != nil {
		return nil, err
	}
	items := append([]Rule(nil), DefaultPlatformMandatoryRules()...)
	if includeFallback {
		items = append(items, DefaultFallbackOptionalRules()...)
	}
	items = append(items, validated...)
	return validateRules(items)
}
