package rules

import (
	"encoding/json"
	"fmt"
	"strings"
)

type RuleStrength string

const (
	RuleMandatory RuleStrength = "mandatory"
	RuleOptional  RuleStrength = "optional"
)

const (
	ReasonLegacyTriggerMiss   = "legacy_trigger_not_matched"
	ReasonSignalsNotMatched   = "activation_not_matched"
	ReasonTokenBudgetExceeded = "token_budget_exceeded"
	ReasonBelowScoreThreshold = "below_score_threshold"
)

type Rule struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Strength        RuleStrength      `json:"strength"`
	Content         string            `json:"content"`
	Triggers        []string          `json:"triggers,omitempty"`
	Activation      Activation        `json:"activation,omitempty"`
	TokenBudget     int               `json:"token_budget,omitempty"`
	Priority        int               `json:"priority,omitempty"`
	SafetyCritical  bool              `json:"safety_critical,omitempty"`
	ManualDependsOn []string          `json:"manual_depends_on,omitempty"`
	PolicyBinding   *PolicyBinding    `json:"policy_binding,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

func (r *Rule) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, legacy := fields["level"]; legacy {
		return fmt.Errorf("rule level is no longer supported; use strength")
	}
	type ruleAlias Rule
	var decoded ruleAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = Rule(decoded)
	return nil
}

// TokenCost exposes the stable cost used by planning and trace accounting.
func (r Rule) TokenCost() int { return ruleCost(r) }

type Activation struct {
	ModeAny      []string `json:"mode_any,omitempty"`
	ModeAll      []string `json:"mode_all,omitempty"`
	RiskAny      []string `json:"risk_any,omitempty"`
	ToolAny      []string `json:"tool_any,omitempty"`
	ToolAll      []string `json:"tool_all,omitempty"`
	TagAny       []string `json:"tag_any,omitempty"`
	TagAll       []string `json:"tag_all,omitempty"`
	KeywordsAny  []string `json:"keywords_any,omitempty"`
	KeywordsAll  []string `json:"keywords_all,omitempty"`
	ExcludeTools []string `json:"exclude_tools,omitempty"`
	ExcludeTags  []string `json:"exclude_tags,omitempty"`
	ExcludeModes []string `json:"exclude_modes,omitempty"`
	ExcludeRisk  []string `json:"exclude_risk,omitempty"`
	MinPriority  int      `json:"min_priority,omitempty"`
	Always       bool     `json:"always,omitempty"`
}

type LoadContext struct {
	Mode         string
	ToolNames    []string
	RiskLevel    string
	Tags         []string
	Task         string
	Conversation string
	TokenBudget  int
	ScoreCutoff  float64
}

type Trace struct {
	Loaded              []string            `json:"loaded,omitempty"`
	Skipped             []string            `json:"skipped,omitempty"`
	SkipReasons         map[string]string   `json:"skip_reasons,omitempty"`
	EstimatedUsed       int                 `json:"estimated_used,omitempty"`
	TokenBudget         int                 `json:"token_budget,omitempty"`
	CandidateCount      int                 `json:"candidate_count,omitempty"`
	ConsideredCount     int                 `json:"considered_count,omitempty"`
	SelectionStrategy   string              `json:"selection_strategy,omitempty"`
	SavedTokens         int                 `json:"saved_tokens,omitempty"`
	RuleScores          map[string]float64  `json:"rule_scores,omitempty"`
	RuleReasons         map[string][]string `json:"rule_reasons,omitempty"`
	MatchedSignals      map[string][]string `json:"matched_signals,omitempty"`
	SkippedSignals      map[string][]string `json:"skipped_signals,omitempty"`
	MandatoryTokens     int                 `json:"mandatory_tokens,omitempty"`
	OptionalBudget      int                 `json:"optional_budget,omitempty"`
	DependencyLoadedBy  map[string][]string `json:"dependency_loaded_by,omitempty"`
	CandidateRoots      []string            `json:"candidate_roots,omitempty"`
	BundleMembers       map[string][]string `json:"bundle_members,omitempty"`
	BundleCosts         map[string]int      `json:"bundle_costs,omitempty"`
	BundleMarginalCosts map[string]int      `json:"bundle_marginal_costs,omitempty"`
	SharedDependencies  map[string][]string `json:"shared_dependencies,omitempty"`
	OptimizerNodes      int                 `json:"optimizer_nodes,omitempty"`
	OptimizerLimited    bool                `json:"optimizer_limited,omitempty"`
	RuleSetID           int64               `json:"rule_set_id,omitempty"`
	RuleSetVersion      string              `json:"rule_set_version,omitempty"`
	CompiledHash        string              `json:"compiled_hash,omitempty"`
}

type PolicyBinding struct {
	PolicyKey string          `json:"policy_key"`
	Params    json.RawMessage `json:"params,omitempty"`
}

func ValidateCustomRules(items []Rule) error {
	_, err := CompileRuleSet(items, CompileOptions{})
	return err
}

type ruleDecision struct {
	rule    Rule
	score   float64
	reasons []string
	signals []string
	cost    int
}

func ruleCost(rule Rule) int {
	if rule.TokenBudget > 0 {
		return rule.TokenBudget
	}
	content := strings.TrimSpace(rule.Content)
	if content == "" {
		return 1
	}
	cost := len([]rune(content)) / 4
	if cost <= 0 {
		return 1
	}
	return cost
}

func isMandatory(rule Rule) bool { return rule.Strength == RuleMandatory }

func evaluateRule(rule Rule, ctx LoadContext) (ruleDecision, bool, string) {
	decision := ruleDecision{rule: rule, cost: ruleCost(rule)}
	if isMandatory(rule) {
		decision.score = compiledOptionalBaseScore + float64(rule.Priority)
		decision.reasons = append(decision.reasons, "mandatory")
		decision.signals = append(decision.signals, "mandatory")
		return decision, true, ""
	}
	if !matchesLegacyTriggers(rule.Triggers, ctx) {
		decision.reasons = append(decision.reasons, "legacy_trigger_miss")
		return decision, false, ReasonLegacyTriggerMiss
	}
	matched, score, reasons, signals := scoreActivation(rule, ctx)
	decision.score = compiledOptionalBaseScore + score + float64(rule.Priority)
	decision.reasons = append(decision.reasons, reasons...)
	decision.signals = append(decision.signals, signals...)
	if !matched {
		return decision, false, ReasonSignalsNotMatched
	}
	return decision, true, ""
}

func matchesLegacyTriggers(triggers []string, ctx LoadContext) bool {
	if len(triggers) == 0 {
		return true
	}
	normalized := loadSignalSet(ctx)
	for _, trigger := range triggers {
		if normalized[strings.ToLower(strings.TrimSpace(trigger))] {
			return true
		}
	}
	return false
}

func loadSignalSet(ctx LoadContext) map[string]bool {
	set := map[string]bool{}
	addSet := func(values ...string) {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "" {
				set[value] = true
			}
		}
	}
	addSet(ctx.Mode, ctx.RiskLevel)
	addSet(ctx.ToolNames...)
	addSet(ctx.Tags...)
	return set
}

func scoreActivation(rule Rule, ctx LoadContext) (bool, float64, []string, []string) {
	act := rule.Activation
	score := 0.0
	reasons := make([]string, 0, 12)
	signals := make([]string, 0, 12)
	matchedAny := false
	if blocked, why := blockedByExclusion(act, ctx); blocked {
		reasons = append(reasons, why)
		return false, score, reasons, signals
	}
	if len(act.ModeAll) > 0 && !containsAll(ctx.Mode, act.ModeAll) {
		reasons = append(reasons, "mode_all_missing")
		return false, score, reasons, signals
	}
	if len(act.ModeAll) > 0 {
		score += 5
		matchedAny = true
		reasons = append(reasons, "mode_all_matched")
		signals = append(signals, "mode_all")
	}
	if len(act.ToolAll) > 0 && !containsAllSet(ctx.ToolNames, act.ToolAll) {
		reasons = append(reasons, "tool_all_missing")
		return false, score, reasons, signals
	}
	if len(act.ToolAll) > 0 {
		score += 5
		matchedAny = true
		reasons = append(reasons, "tool_all_matched")
		signals = append(signals, "tool_all")
	}
	if len(act.TagAll) > 0 && !containsAllSet(ctx.Tags, act.TagAll) {
		reasons = append(reasons, "tag_all_missing")
		return false, score, reasons, signals
	}
	if len(act.TagAll) > 0 {
		score += 4
		matchedAny = true
		reasons = append(reasons, "tag_all_matched")
		signals = append(signals, "tag_all")
	}
	if len(act.KeywordsAll) > 0 && !containsAllKeywords(ctx, act.KeywordsAll) {
		reasons = append(reasons, "keyword_all_missing")
		return false, score, reasons, signals
	}
	if len(act.KeywordsAll) > 0 {
		score += 3
		matchedAny = true
		reasons = append(reasons, "keyword_all_matched")
		signals = append(signals, "keyword_all")
	}
	if act.MinPriority > 0 && rule.Priority < act.MinPriority {
		reasons = append(reasons, "priority_below_min")
		return false, score, reasons, signals
	}
	if act.Always {
		score += 6
		matchedAny = true
		reasons = append(reasons, "activation.always")
		signals = append(signals, "always")
	}
	if len(act.ModeAny) > 0 {
		if containsAny(ctx.Mode, act.ModeAny) {
			score += 5
			matchedAny = true
			reasons = append(reasons, "mode_any_matched")
			signals = append(signals, fmt.Sprintf("mode:%s", strings.ToLower(strings.TrimSpace(ctx.Mode))))
		} else {
			reasons = append(reasons, "mode_any_missing")
		}
	}
	if len(act.RiskAny) > 0 {
		if containsAny(ctx.RiskLevel, act.RiskAny) {
			score += 4
			matchedAny = true
			reasons = append(reasons, "risk_any_matched")
			signals = append(signals, fmt.Sprintf("risk:%s", strings.ToLower(strings.TrimSpace(ctx.RiskLevel))))
		} else {
			reasons = append(reasons, "risk_any_missing")
		}
	}
	if len(act.ToolAny) > 0 {
		if signal, ok := matchSetSignal(ctx.ToolNames, act.ToolAny); ok {
			score += 5
			matchedAny = true
			reasons = append(reasons, "tool_any_matched")
			signals = append(signals, "tool:"+signal)
		} else {
			reasons = append(reasons, "tool_any_missing")
		}
	}
	if len(act.TagAny) > 0 {
		if signal, ok := matchSetSignal(ctx.Tags, act.TagAny); ok {
			score += 4
			matchedAny = true
			reasons = append(reasons, "tag_any_matched")
			signals = append(signals, "tag:"+signal)
		} else {
			reasons = append(reasons, "tag_any_missing")
		}
	}
	if len(act.KeywordsAny) > 0 {
		if signal, ok := matchKeywordSignal(ctx, act.KeywordsAny); ok {
			score += 3
			matchedAny = true
			reasons = append(reasons, "keyword_any_matched")
			signals = append(signals, "keyword:"+signal)
		} else {
			reasons = append(reasons, "keyword_any_missing")
		}
	}
	if !hasPositiveActivation(act) {
		reasons = append(reasons, "dependency_only")
	}
	return matchedAny, score, reasons, dedupeStrings(signals)
}

func blockedByExclusion(act Activation, ctx LoadContext) (bool, string) {
	if containsAny(ctx.Mode, act.ExcludeModes) {
		return true, "mode_excluded"
	}
	if containsAny(ctx.RiskLevel, act.ExcludeRisk) {
		return true, "risk_excluded"
	}
	if _, ok := matchSetSignal(ctx.ToolNames, act.ExcludeTools); ok {
		return true, "tool_excluded"
	}
	if _, ok := matchSetSignal(ctx.Tags, act.ExcludeTags); ok {
		return true, "tag_excluded"
	}
	return false, ""
}

func noActivationHints(act Activation) bool {
	return len(act.ModeAny) == 0 && len(act.ModeAll) == 0 && len(act.RiskAny) == 0 && len(act.ToolAny) == 0 && len(act.ToolAll) == 0 && len(act.TagAny) == 0 && len(act.TagAll) == 0 && len(act.KeywordsAny) == 0 && len(act.KeywordsAll) == 0 && len(act.ExcludeTools) == 0 && len(act.ExcludeTags) == 0 && len(act.ExcludeModes) == 0 && len(act.ExcludeRisk) == 0 && !act.Always && act.MinPriority == 0
}

func hasPositiveActivation(act Activation) bool {
	return act.Always || len(act.ModeAny) > 0 || len(act.ModeAll) > 0 || len(act.RiskAny) > 0 || len(act.ToolAny) > 0 || len(act.ToolAll) > 0 || len(act.TagAny) > 0 || len(act.TagAll) > 0 || len(act.KeywordsAny) > 0 || len(act.KeywordsAll) > 0
}

func containsAny(value string, candidates []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, candidate := range candidates {
		if value == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func containsAll(value string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, item := range required {
		if value != strings.ToLower(strings.TrimSpace(item)) {
			return false
		}
	}
	return true
}

func containsAllSet(values, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := normalizedSet(values)
	for _, item := range required {
		if !set[strings.ToLower(strings.TrimSpace(item))] {
			return false
		}
	}
	return true
}

func containsAllKeywords(ctx LoadContext, required []string) bool {
	body := strings.ToLower(strings.TrimSpace(ctx.Task + "\n" + ctx.Conversation))
	if body == "" {
		return false
	}
	for _, keyword := range required {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		if !strings.Contains(body, keyword) {
			return false
		}
	}
	return true
}

func matchSetSignal(values, candidates []string) (string, bool) {
	set := normalizedSet(values)
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && set[candidate] {
			return candidate, true
		}
	}
	return "", false
}

func matchKeywordSignal(ctx LoadContext, candidates []string) (string, bool) {
	body := strings.ToLower(strings.TrimSpace(ctx.Task + "\n" + ctx.Conversation))
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && strings.Contains(body, candidate) {
			return candidate, true
		}
	}
	return "", false
}

func normalizedSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func dedupeStrings(values []string) []string {
	if len(values) <= 1 {
		return values
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (t *Trace) skip(ruleID, reason string) {
	t.Skipped = append(t.Skipped, ruleID)
	if reason == "" {
		return
	}
	if t.SkipReasons == nil {
		t.SkipReasons = map[string]string{}
	}
	t.SkipReasons[ruleID] = reason
}

func (t *Trace) unskip(ruleID string) {
	for index := 0; index < len(t.Skipped); {
		if t.Skipped[index] == ruleID {
			t.Skipped = append(t.Skipped[:index], t.Skipped[index+1:]...)
			continue
		}
		index++
	}
	if t.SkipReasons != nil {
		delete(t.SkipReasons, ruleID)
	}
}

func (t *Trace) noteScore(ruleID string, score float64) {
	if t.RuleScores == nil {
		t.RuleScores = map[string]float64{}
	}
	t.RuleScores[ruleID] = score
}

func (t *Trace) noteReasons(ruleID string, reasons []string) {
	if len(reasons) == 0 {
		return
	}
	if t.RuleReasons == nil {
		t.RuleReasons = map[string][]string{}
	}
	t.RuleReasons[ruleID] = dedupeStrings(append(t.RuleReasons[ruleID], reasons...))
}

func (t *Trace) noteSignals(ruleID string, signals []string) {
	if len(signals) == 0 {
		return
	}
	if t.MatchedSignals == nil {
		t.MatchedSignals = map[string][]string{}
	}
	t.MatchedSignals[ruleID] = dedupeStrings(append(t.MatchedSignals[ruleID], signals...))
}

func (t *Trace) noteSkippedSignals(ruleID string, signals []string) {
	if len(signals) == 0 {
		return
	}
	if t.SkippedSignals == nil {
		t.SkippedSignals = map[string][]string{}
	}
	t.SkippedSignals[ruleID] = dedupeStrings(append(t.SkippedSignals[ruleID], signals...))
}
