package rules

import "strings"

type RuleLevel string

const (
	LevelL1Core      RuleLevel = "l1_core"
	LevelL2Scenario  RuleLevel = "l2_scenario"
	LevelL3Ephemeral RuleLevel = "l3_ephemeral"
)

type Rule struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Level       RuleLevel         `json:"level"`
	Content     string            `json:"content"`
	Triggers    []string          `json:"triggers,omitempty"`
	TokenBudget int               `json:"token_budget,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Trace struct {
	Loaded  []string `json:"loaded,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Levels  []string `json:"levels,omitempty"`
}

type Registry struct {
	rules []Rule
}

func NewRegistry(items ...Rule) *Registry {
	r := &Registry{}
	for _, item := range items {
		r.Register(item)
	}
	return r
}

func (r *Registry) Register(rule Rule) {
	if r == nil || strings.TrimSpace(rule.ID) == "" {
		return
	}
	if rule.Level == "" {
		rule.Level = LevelL2Scenario
	}
	r.rules = append(r.rules, rule)
}

func (r *Registry) Load(ctx LoadContext) ([]Rule, Trace) {
	if r == nil {
		return nil, Trace{}
	}
	loaded := make([]Rule, 0, len(r.rules))
	trace := Trace{}
	for _, rule := range r.rules {
		if shouldLoad(rule, ctx) {
			loaded = append(loaded, rule)
			trace.Loaded = append(trace.Loaded, rule.ID)
			trace.Levels = append(trace.Levels, string(rule.Level))
			continue
		}
		trace.Skipped = append(trace.Skipped, rule.ID)
	}
	return loaded, trace
}

type LoadContext struct {
	Mode      string
	ToolNames []string
	RiskLevel string
	Tags      []string
}

func shouldLoad(rule Rule, ctx LoadContext) bool {
	if rule.Level == LevelL1Core {
		return true
	}
	if len(rule.Triggers) == 0 {
		return rule.Level != LevelL3Ephemeral
	}
	for _, trigger := range rule.Triggers {
		trigger = strings.TrimSpace(strings.ToLower(trigger))
		if trigger == "" {
			continue
		}
		if trigger == strings.ToLower(strings.TrimSpace(ctx.Mode)) || trigger == strings.ToLower(strings.TrimSpace(ctx.RiskLevel)) {
			return true
		}
		for _, toolName := range ctx.ToolNames {
			if trigger == strings.ToLower(strings.TrimSpace(toolName)) {
				return true
			}
		}
		for _, tag := range ctx.Tags {
			if trigger == strings.ToLower(strings.TrimSpace(tag)) {
				return true
			}
		}
	}
	return false
}
