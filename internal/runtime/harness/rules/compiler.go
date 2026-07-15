package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	PolicyDangerousArgumentsDeny = "tool.dangerous_arguments.deny"
	PolicyRiskRequireApproval    = "tool.risk.require_approval"
	PolicyHostAllowlist          = "tool.host.allowlist"
	PolicyExecutionLimits        = "tool.execution_limits"

	DefaultTokenEstimatorVersion = "conservative-rune-v1"
	DefaultOptimizerExpansions   = 4096
	compiledOptionalBaseScore    = 300.0
	CurrentSnapshotSchemaVersion = 2
	maxRuleCount                 = 50
	maxRuleEdges                 = 200
	maxRuleDepth                 = 16
	maxRuleContentBytes          = 16 * 1024
)

var ruleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type DependencyEdge struct {
	RuleID     string  `json:"rule_id"`
	DependsOn  string  `json:"depends_on"`
	Source     string  `json:"source,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Decision   string  `json:"decision,omitempty"`
}

type CompileOptions struct {
	RuleSetID             int64
	Version               string
	Edges                 []DependencyEdge
	TokenEstimatorVersion string
}

type BoundPolicyBinding struct {
	RuleID    string          `json:"rule_id"`
	PolicyKey string          `json:"policy_key"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type CompiledRule struct {
	Rule                  Rule              `json:"rule"`
	DependsOn             []string          `json:"depends_on,omitempty"`
	DependencySources     map[string]string `json:"dependency_sources,omitempty"`
	DependencyClosure     []string          `json:"dependency_closure,omitempty"`
	DependencyClosureBits []uint64          `json:"dependency_closure_bits,omitempty"`
	TokenCost             int               `json:"token_cost"`
	TopologicalOrder      int               `json:"topological_order"`
	ContentHash           string            `json:"content_hash"`
}

type CompiledRuleSet struct {
	SchemaVersion         int            `json:"schema_version"`
	ID                    int64          `json:"id,omitempty"`
	Version               string         `json:"version,omitempty"`
	CompiledHash          string         `json:"compiled_hash"`
	TokenEstimatorVersion string         `json:"token_estimator_version"`
	MandatoryTokens       int            `json:"mandatory_tokens"`
	Rules                 []CompiledRule `json:"rules"`

	byID           map[string]int
	legacyVerified bool
	legacyRaw      json.RawMessage
}

func (c CompiledRuleSet) MarshalJSON() ([]byte, error) {
	if c.SchemaVersion < CurrentSnapshotSchemaVersion && len(c.legacyRaw) > 0 {
		return append([]byte(nil), c.legacyRaw...), nil
	}
	type compiledRuleSetAlias CompiledRuleSet
	return json.Marshal(compiledRuleSetAlias(c))
}

func (c *CompiledRuleSet) Prepare() {
	if c == nil {
		return
	}
	c.byID = make(map[string]int, len(c.Rules))
	for index := range c.Rules {
		c.byID[c.Rules[index].Rule.ID] = index
	}
}

func (c *CompiledRuleSet) RuleByID(id string) (CompiledRule, bool) {
	if c == nil {
		return CompiledRule{}, false
	}
	if c.byID == nil {
		c.Prepare()
	}
	index, ok := c.byID[id]
	if !ok {
		return CompiledRule{}, false
	}
	return c.Rules[index], true
}

func (c *CompiledRuleSet) PolicyBindings() []BoundPolicyBinding {
	if c == nil {
		return nil
	}
	bindings := make([]BoundPolicyBinding, 0)
	for _, item := range c.Rules {
		if item.Rule.PolicyBinding == nil || item.Rule.Strength != RuleMandatory {
			continue
		}
		bindings = append(bindings, BoundPolicyBinding{
			RuleID: item.Rule.ID, PolicyKey: item.Rule.PolicyBinding.PolicyKey,
			Params: append(json.RawMessage(nil), item.Rule.PolicyBinding.Params...),
		})
	}
	return bindings
}

func RulesFromCompiled(c *CompiledRuleSet) []Rule {
	if c == nil {
		return nil
	}
	items := make([]Rule, 0, len(c.Rules))
	for _, compiled := range c.Rules {
		rule := compiled.Rule
		rule.ManualDependsOn = append([]string(nil), compiled.DependsOn...)
		items = append(items, rule)
	}
	return items
}

func PolicyBindingsForRules(items []Rule) []BoundPolicyBinding {
	bindings := make([]BoundPolicyBinding, 0)
	for _, rule := range items {
		if rule.PolicyBinding == nil || rule.Strength != RuleMandatory {
			continue
		}
		bindings = append(bindings, BoundPolicyBinding{
			RuleID: rule.ID, PolicyKey: rule.PolicyBinding.PolicyKey,
			Params: append(json.RawMessage(nil), rule.PolicyBinding.Params...),
		})
	}
	return bindings
}

func CompileRuleSet(items []Rule, opts CompileOptions) (*CompiledRuleSet, error) {
	if len(items) > maxRuleCount {
		return nil, fmt.Errorf("at most %d custom rules are allowed", maxRuleCount)
	}
	if opts.TokenEstimatorVersion == "" {
		opts.TokenEstimatorVersion = DefaultTokenEstimatorVersion
	}

	normalized := make([]Rule, len(items))
	byID := make(map[string]int, len(items))
	for index, input := range items {
		rule := input
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Content = strings.TrimSpace(rule.Content)
		if !ruleIDPattern.MatchString(rule.ID) {
			return nil, fmt.Errorf("custom rule id %q is invalid", rule.ID)
		}
		if rule.Content == "" {
			return nil, fmt.Errorf("custom rule %q requires content", rule.ID)
		}
		if len(rule.Content) > maxRuleContentBytes {
			return nil, fmt.Errorf("custom rule %q content exceeds %d bytes", rule.ID, maxRuleContentBytes)
		}
		if _, exists := byID[rule.ID]; exists {
			return nil, fmt.Errorf("duplicate custom rule id %q", rule.ID)
		}
		if rule.Strength != RuleMandatory && rule.Strength != RuleOptional {
			return nil, fmt.Errorf("custom rule %q requires strength mandatory or optional", rule.ID)
		}
		if err := validatePolicyBinding(rule); err != nil {
			return nil, err
		}
		rule.ManualDependsOn = stableUnique(rule.ManualDependsOn)
		byID[rule.ID] = index
		normalized[index] = rule
	}

	edges := make([]DependencyEdge, 0, len(opts.Edges)+len(items))
	for _, rule := range normalized {
		for _, dependency := range rule.ManualDependsOn {
			edges = append(edges, DependencyEdge{RuleID: rule.ID, DependsOn: dependency, Source: "manual", Decision: "accepted"})
		}
	}
	for _, edge := range opts.Edges {
		if edge.Decision == "" || edge.Decision == "accepted" {
			edges = append(edges, edge)
		}
	}
	if len(edges) > maxRuleEdges {
		return nil, fmt.Errorf("rule dependencies exceed %d edges", maxRuleEdges)
	}

	dependencies := make(map[string][]string, len(items))
	dependencySources := make(map[string]map[string]string, len(items))
	adjacency := make(map[string][]string, len(items))
	indegree := make(map[string]int, len(items))
	for _, rule := range normalized {
		indegree[rule.ID] = 0
	}
	seenEdges := map[string]bool{}
	for _, edge := range edges {
		ruleID := strings.TrimSpace(edge.RuleID)
		dependencyID := strings.TrimSpace(edge.DependsOn)
		ruleIndex, ruleExists := byID[ruleID]
		dependencyIndex, dependencyExists := byID[dependencyID]
		if !ruleExists || !dependencyExists {
			return nil, fmt.Errorf("rule dependency %q -> %q references an unknown rule", ruleID, dependencyID)
		}
		if ruleID == dependencyID {
			return nil, fmt.Errorf("rule %q cannot depend on itself", ruleID)
		}
		key := ruleID + "\x00" + dependencyID
		if seenEdges[key] {
			if normalizeDependencySource(edge.Source) == "manual" {
				dependencySources[ruleID][dependencyID] = "manual"
			}
			continue
		}
		seenEdges[key] = true
		if normalized[ruleIndex].Strength == RuleMandatory && normalized[dependencyIndex].Strength == RuleOptional {
			return nil, fmt.Errorf("mandatory rule %q cannot depend on optional rule %q", ruleID, dependencyID)
		}
		dependencies[ruleID] = append(dependencies[ruleID], dependencyID)
		if dependencySources[ruleID] == nil {
			dependencySources[ruleID] = map[string]string{}
		}
		dependencySources[ruleID][dependencyID] = normalizeDependencySource(edge.Source)
		adjacency[dependencyID] = append(adjacency[dependencyID], ruleID)
		indegree[ruleID]++
	}
	for id := range dependencies {
		sort.Strings(dependencies[id])
	}
	for id := range adjacency {
		sort.Strings(adjacency[id])
	}
	for _, rule := range normalized {
		if rule.Strength == RuleOptional && !hasPositiveActivation(rule.Activation) && len(adjacency[rule.ID]) == 0 {
			return nil, fmt.Errorf("optional rule %q requires activation or must be referenced as a dependency", rule.ID)
		}
	}

	topologicalIDs := make([]string, 0, len(items))
	ready := make([]string, 0, len(items))
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sortReadyRules(ready, normalized, byID)
	depth := make(map[string]int, len(items))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		topologicalIDs = append(topologicalIDs, id)
		for _, next := range adjacency[id] {
			if depth[next] < depth[id]+1 {
				depth[next] = depth[id] + 1
			}
			if depth[next] > maxRuleDepth {
				return nil, fmt.Errorf("rule dependency depth exceeds %d at %q", maxRuleDepth, next)
			}
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
				sortReadyRules(ready, normalized, byID)
			}
		}
	}
	if len(topologicalIDs) != len(items) {
		return nil, fmt.Errorf("rule dependency graph contains a cycle")
	}

	closures := make(map[string][]string, len(items))
	topologicalIndex := make(map[string]int, len(topologicalIDs))
	for index, id := range topologicalIDs {
		topologicalIndex[id] = index
	}
	compiled := &CompiledRuleSet{
		SchemaVersion:         CurrentSnapshotSchemaVersion,
		ID:                    opts.RuleSetID,
		Version:               strings.TrimSpace(opts.Version),
		TokenEstimatorVersion: opts.TokenEstimatorVersion,
		Rules:                 make([]CompiledRule, 0, len(items)),
	}
	for order, id := range topologicalIDs {
		rule := normalized[byID[id]]
		closureSet := map[string]bool{}
		for _, dependencyID := range dependencies[id] {
			closureSet[dependencyID] = true
			for _, transitiveID := range closures[dependencyID] {
				closureSet[transitiveID] = true
			}
		}
		closure := make([]string, 0, len(closureSet))
		for _, topoID := range topologicalIDs[:order] {
			if closureSet[topoID] {
				closure = append(closure, topoID)
			}
		}
		closures[id] = closure
		closureBits := make([]uint64, (len(topologicalIDs)+63)/64)
		for _, dependencyID := range closure {
			dependencyIndex := topologicalIndex[dependencyID]
			closureBits[dependencyIndex/64] |= uint64(1) << uint(dependencyIndex%64)
		}
		directSources := make(map[string]string, len(dependencies[id]))
		for _, dependencyID := range dependencies[id] {
			directSources[dependencyID] = dependencySources[id][dependencyID]
		}
		cost := conservativeRuleCost(rule)
		if rule.Strength == RuleMandatory {
			compiled.MandatoryTokens += cost
		}
		compiled.Rules = append(compiled.Rules, CompiledRule{
			Rule:                  rule,
			DependsOn:             append([]string(nil), dependencies[id]...),
			DependencySources:     directSources,
			DependencyClosure:     closure,
			DependencyClosureBits: closureBits,
			TokenCost:             cost,
			TopologicalOrder:      order,
			ContentHash:           hashText(rule.Content),
		})
	}
	if err := RefreshCompiledHash(compiled); err != nil {
		return nil, err
	}
	compiled.Prepare()
	return compiled, nil
}

// RefreshCompiledHash must be called after changing snapshot identity fields.
func RefreshCompiledHash(compiled *CompiledRuleSet) error {
	if compiled == nil {
		return fmt.Errorf("compiled rule set is nil")
	}
	canonical := *compiled
	canonical.CompiledHash = ""
	canonical.legacyRaw = nil
	data, err := json.Marshal(&canonical)
	if err != nil {
		return fmt.Errorf("marshal compiled rule set: %w", err)
	}
	sum := sha256.Sum256(data)
	compiled.CompiledHash = hex.EncodeToString(sum[:])
	return nil
}

func sortReadyRules(ready []string, rules []Rule, byID map[string]int) {
	sort.SliceStable(ready, func(i, j int) bool {
		left := rules[byID[ready[i]]]
		right := rules[byID[ready[j]]]
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		return left.ID < right.ID
	})
}

func normalizeDependencySource(source string) string {
	if strings.EqualFold(strings.TrimSpace(source), "manual") {
		return "manual"
	}
	return "llm_confirmed"
}

// VerifyCompiledHash recomputes the canonical snapshot hash instead of trusting
// the hash embedded in a persisted snapshot. This detects content tampering even
// when the database hash column and the embedded hash were changed together.
func VerifyCompiledHash(compiled *CompiledRuleSet) error {
	if compiled == nil {
		return fmt.Errorf("compiled rule set is nil")
	}
	if compiled.SchemaVersion < CurrentSnapshotSchemaVersion {
		if compiled.legacyVerified {
			return nil
		}
		return fmt.Errorf("legacy compiled rule set was not decoded by the compatibility codec")
	}
	expected := strings.TrimSpace(compiled.CompiledHash)
	if expected == "" {
		return fmt.Errorf("compiled rule set hash is empty")
	}
	canonical := *compiled
	if err := RefreshCompiledHash(&canonical); err != nil {
		return fmt.Errorf("compute compiled rule set hash for verification: %w", err)
	}
	actual := canonical.CompiledHash
	if actual != expected {
		return fmt.Errorf("compiled rule set hash mismatch")
	}
	return nil
}

func validatePolicyBinding(rule Rule) error {
	if rule.PolicyBinding == nil {
		if rule.SafetyCritical {
			return fmt.Errorf("safety-critical rule %q requires a policy binding", rule.ID)
		}
		return nil
	}
	if rule.Strength != RuleMandatory {
		return fmt.Errorf("rule %q with a policy binding must be mandatory", rule.ID)
	}
	policyKey := strings.TrimSpace(rule.PolicyBinding.PolicyKey)
	switch policyKey {
	case PolicyDangerousArgumentsDeny, PolicyRiskRequireApproval, PolicyHostAllowlist, PolicyExecutionLimits:
	default:
		return fmt.Errorf("rule %q uses unknown policy binding %q", rule.ID, rule.PolicyBinding.PolicyKey)
	}
	if len(rule.PolicyBinding.Params) > 0 && string(rule.PolicyBinding.Params) != "null" {
		var object map[string]any
		if err := json.Unmarshal(rule.PolicyBinding.Params, &object); err != nil || object == nil {
			return fmt.Errorf("rule %q policy binding params must be a JSON object", rule.ID)
		}
	}
	if err := validateTypedPolicyParams(policyKey, rule.PolicyBinding.Params); err != nil {
		return fmt.Errorf("rule %q policy binding: %w", rule.ID, err)
	}
	return nil
}

func validateTypedPolicyParams(policyKey string, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	decode := func(target any) error {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		return decoder.Decode(target)
	}
	switch policyKey {
	case PolicyDangerousArgumentsDeny:
		var params struct{}
		return decode(&params)
	case PolicyRiskRequireApproval:
		var params struct {
			RiskLevels []string `json:"risk_levels"`
		}
		if err := decode(&params); err != nil {
			return fmt.Errorf("risk_levels must be an array: %w", err)
		}
		if len(stableUnique(params.RiskLevels)) == 0 {
			return fmt.Errorf("risk_levels must not be empty")
		}
	case PolicyHostAllowlist:
		var params struct {
			AllowedHosts []string `json:"allowed_hosts"`
		}
		if err := decode(&params); err != nil {
			return fmt.Errorf("allowed_hosts must be an array: %w", err)
		}
		if len(stableUnique(params.AllowedHosts)) == 0 {
			return fmt.Errorf("allowed_hosts must not be empty")
		}
	case PolicyExecutionLimits:
		var params struct {
			MaxToolTimeoutMS   int `json:"max_tool_timeout_ms"`
			MaxToolOutputBytes int `json:"max_tool_output_bytes"`
		}
		if err := decode(&params); err != nil {
			return err
		}
		if params.MaxToolTimeoutMS < 0 || params.MaxToolTimeoutMS > 600000 {
			return fmt.Errorf("max_tool_timeout_ms must be 0..600000")
		}
		if params.MaxToolOutputBytes < 0 || params.MaxToolOutputBytes > 2*1024*1024 {
			return fmt.Errorf("max_tool_output_bytes must be 0..2097152")
		}
		if params.MaxToolTimeoutMS == 0 && params.MaxToolOutputBytes == 0 {
			return fmt.Errorf("execution limits must set at least one positive limit")
		}
	}
	return nil
}

func conservativeRuleCost(rule Rule) int {
	count := len([]rune(strings.TrimSpace(rule.Content)))
	if count < 1 {
		count = 1
	}
	if rule.TokenBudget > count {
		return rule.TokenBudget
	}
	return count
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stableUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

type optionalCandidate struct {
	root       string
	score      float64
	members    []string
	memberCost int
}

func SelectMandatoryRules(compiled *CompiledRuleSet) ([]Rule, Trace) {
	trace := Trace{SelectionStrategy: "mandatory_static:v2"}
	if compiled == nil {
		return nil, trace
	}
	trace.MandatoryTokens = compiled.MandatoryTokens
	trace.RuleSetID = compiled.ID
	trace.RuleSetVersion = compiled.Version
	trace.CompiledHash = compiled.CompiledHash
	loaded := make([]Rule, 0)
	for _, item := range compiled.Rules {
		if item.Rule.Strength != RuleMandatory {
			continue
		}
		loaded = append(loaded, item.Rule)
		trace.Loaded = append(trace.Loaded, item.Rule.ID)
		trace.EstimatedUsed += item.TokenCost
	}
	return loaded, trace
}

func SelectOptionalRules(compiled *CompiledRuleSet, ctx LoadContext, maxExpansions int) ([]Rule, Trace) {
	trace := Trace{TokenBudget: ctx.TokenBudget, OptionalBudget: ctx.TokenBudget, SelectionStrategy: "dag_branch_and_bound:v1"}
	if compiled == nil {
		return nil, trace
	}
	trace.MandatoryTokens = compiled.MandatoryTokens
	trace.RuleSetID = compiled.ID
	trace.RuleSetVersion = compiled.Version
	trace.CompiledHash = compiled.CompiledHash
	trace.BundleMembers = map[string][]string{}
	trace.BundleCosts = map[string]int{}
	trace.BundleMarginalCosts = map[string]int{}
	trace.DependencyLoadedBy = map[string][]string{}
	trace.SharedDependencies = map[string][]string{}
	if maxExpansions <= 0 {
		maxExpansions = DefaultOptimizerExpansions
	}

	candidates := make([]optionalCandidate, 0, len(compiled.Rules))
	for _, item := range compiled.Rules {
		rule := item.Rule
		if rule.Strength != RuleOptional {
			continue
		}
		decision, matched, reason := evaluateRule(rule, ctx)
		if !matched {
			trace.skip(rule.ID, reason)
			trace.noteReasons(rule.ID, decision.reasons)
			trace.noteSkippedSignals(rule.ID, decision.signals)
			continue
		}
		if ctx.ScoreCutoff > 0 && decision.score < ctx.ScoreCutoff {
			trace.skip(rule.ID, ReasonBelowScoreThreshold)
			continue
		}
		trace.CandidateCount++
		trace.noteScore(rule.ID, decision.score)
		trace.noteReasons(rule.ID, decision.reasons)
		trace.noteSignals(rule.ID, decision.signals)
		members := make([]string, 0, len(item.DependencyClosure)+1)
		members = append(members, item.DependencyClosure...)
		members = append(members, rule.ID)
		cost := 0
		optionalMembers := members[:0]
		for _, memberID := range members {
			member, ok := compiled.RuleByID(memberID)
			if !ok || member.Rule.Strength == RuleMandatory {
				continue
			}
			optionalMembers = append(optionalMembers, memberID)
			cost += member.TokenCost
		}
		members = append([]string(nil), optionalMembers...)
		trace.BundleMembers[rule.ID] = append([]string(nil), members...)
		trace.BundleCosts[rule.ID] = cost
		trace.CandidateRoots = append(trace.CandidateRoots, rule.ID)
		candidates = append(candidates, optionalCandidate{root: rule.ID, score: decision.score, members: members, memberCost: cost})
	}
	trace.ConsideredCount = len(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].root < candidates[j].root
		}
		return candidates[i].score > candidates[j].score
	})

	remainingScore := make([]float64, len(candidates)+1)
	for index := len(candidates) - 1; index >= 0; index-- {
		remainingScore[index] = remainingScore[index+1] + candidates[index].score
	}
	bestScore := -1.0
	bestCost := 0
	bestRoots := map[string]bool{}
	bestMembers := map[string]bool{}
	expansions := 0
	limited := false
	var search func(int, float64, int, map[string]bool, map[string]bool)
	search = func(index int, score float64, cost int, roots, members map[string]bool) {
		if expansions >= maxExpansions {
			limited = true
			return
		}
		expansions++
		if score+remainingScore[index] < bestScore {
			return
		}
		if index == len(candidates) {
			if score > bestScore || (score == bestScore && cost < bestCost) {
				bestScore = score
				bestCost = cost
				bestRoots = cloneBoolMap(roots)
				bestMembers = cloneBoolMap(members)
			}
			return
		}
		candidate := candidates[index]
		incremental := 0
		included := make([]string, 0, len(candidate.members))
		for _, memberID := range candidate.members {
			if members[memberID] {
				continue
			}
			member, _ := compiled.RuleByID(memberID)
			incremental += member.TokenCost
			included = append(included, memberID)
		}
		if ctx.TokenBudget > 0 && cost+incremental <= ctx.TokenBudget {
			for _, memberID := range included {
				members[memberID] = true
			}
			roots[candidate.root] = true
			search(index+1, score+candidate.score, cost+incremental, roots, members)
			delete(roots, candidate.root)
			for _, memberID := range included {
				delete(members, memberID)
			}
		}
		search(index+1, score, cost, roots, members)
	}
	search(0, 0, 0, map[string]bool{}, map[string]bool{})
	trace.OptimizerNodes = expansions
	trace.OptimizerLimited = limited

	loaded := make([]Rule, 0, len(bestMembers))
	for _, item := range compiled.Rules {
		if !bestMembers[item.Rule.ID] {
			continue
		}
		loaded = append(loaded, item.Rule)
		trace.unskip(item.Rule.ID)
		trace.Loaded = append(trace.Loaded, item.Rule.ID)
	}
	trace.EstimatedUsed = bestCost
	marginalMembers := map[string]bool{}
	for _, candidate := range candidates {
		if !bestRoots[candidate.root] {
			if !bestMembers[candidate.root] {
				trace.skip(candidate.root, ReasonTokenBudgetExceeded)
			}
			continue
		}
		marginalCost := 0
		for _, memberID := range candidate.members {
			if !marginalMembers[memberID] {
				member, _ := compiled.RuleByID(memberID)
				marginalCost += member.TokenCost
				marginalMembers[memberID] = true
			}
			if memberID == candidate.root {
				continue
			}
			trace.DependencyLoadedBy[memberID] = stableUnique(append(trace.DependencyLoadedBy[memberID], candidate.root))
		}
		trace.BundleMarginalCosts[candidate.root] = marginalCost
	}
	for dependencyID, roots := range trace.DependencyLoadedBy {
		if len(roots) > 1 {
			trace.SharedDependencies[dependencyID] = append([]string(nil), roots...)
		}
	}
	return loaded, trace
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	output := make(map[string]bool, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
