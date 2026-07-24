package rules

import "sort"

func SelectMandatoryRules(items []Rule) ([]Rule, Trace) {
	loaded := make([]Rule, 0)
	trace := Trace{}
	for _, rule := range items {
		if rule.Strength != RuleMandatory {
			continue
		}
		loaded = append(loaded, rule)
		trace.Loaded = append(trace.Loaded, rule.ID)
		trace.EstimatedUsed += ruleCost(rule)
	}
	trace.MandatoryTokens = trace.EstimatedUsed
	return loaded, trace
}

func SelectOptionalRules(items []Rule, ctx LoadContext) ([]Rule, Trace) {
	trace := Trace{TokenBudget: ctx.TokenBudget, OptionalBudget: ctx.TokenBudget}
	type candidate struct {
		rule Rule
		cost int
	}
	candidates := make([]candidate, 0, len(items))
	for _, rule := range items {
		if rule.Strength != RuleOptional {
			continue
		}
		if !matches(rule, ctx) {
			trace.skip(rule.ID, ReasonSignalsNotMatched)
			continue
		}
		cost := ruleCost(rule)
		if actual := ctx.RuleTokenCosts[rule.ID]; actual > 0 {
			cost = actual
		}
		candidates = append(candidates, candidate{rule: rule, cost: cost})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rule.Priority != candidates[j].rule.Priority {
			return candidates[i].rule.Priority > candidates[j].rule.Priority
		}
		if candidates[i].cost != candidates[j].cost {
			return candidates[i].cost < candidates[j].cost
		}
		return candidates[i].rule.ID < candidates[j].rule.ID
	})
	loaded := make([]Rule, 0, len(candidates))
	for _, candidate := range candidates {
		if ctx.TokenBudget <= 0 || trace.EstimatedUsed+candidate.cost > ctx.TokenBudget {
			trace.skip(candidate.rule.ID, ReasonTokenBudgetExceeded)
			continue
		}
		loaded = append(loaded, candidate.rule)
		trace.Loaded = append(trace.Loaded, candidate.rule.ID)
		trace.EstimatedUsed += candidate.cost
	}
	return loaded, trace
}
