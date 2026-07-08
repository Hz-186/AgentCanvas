package rules

type AuditPolicy struct {
	MinEvaluations int
	MinHitRate     float64
}

type RuleAudit struct {
	Evaluations int     `json:"evaluations"`
	Hits        int     `json:"hits"`
	HitRate     float64 `json:"hit_rate"`
	Pruned      bool    `json:"pruned"`
}

type AuditStore struct {
	stats map[string]RuleAudit
}

func NewAuditStore() *AuditStore {
	return &AuditStore{stats: map[string]RuleAudit{}}
}

func (s *AuditStore) Record(ruleID string, hit bool) RuleAudit {
	if s == nil || ruleID == "" {
		return RuleAudit{}
	}
	if s.stats == nil {
		s.stats = map[string]RuleAudit{}
	}
	stat := s.stats[ruleID]
	stat.Evaluations++
	if hit {
		stat.Hits++
	}
	stat.HitRate = float64(stat.Hits) / float64(stat.Evaluations)
	s.stats[ruleID] = stat
	return stat
}

func (s *AuditStore) Snapshot(ruleID string) RuleAudit {
	if s == nil || s.stats == nil {
		return RuleAudit{}
	}
	return s.stats[ruleID]
}

func ShouldPrune(rule Rule, audit RuleAudit, policy AuditPolicy) bool {
	if hardPinnedLevel(rule.Level) {
		return false
	}
	if rule.Level != LevelL2Scenario && rule.Level != LevelL3Tool && rule.Level != LevelL4Ephemeral {
		return false
	}
	if policy.MinEvaluations <= 0 {
		policy.MinEvaluations = 20
	}
	if policy.MinHitRate <= 0 {
		policy.MinHitRate = 0.05
	}
	return audit.Evaluations >= policy.MinEvaluations && audit.HitRate < policy.MinHitRate
}

func (r *Registry) LoadWithAudit(ctx LoadContext, audit *AuditStore, policy AuditPolicy) ([]Rule, Trace) {
	loaded, trace := r.Load(ctx)
	if audit == nil {
		return loaded, trace
	}
	kept := loaded[:0]
	for _, rule := range loaded {
		stat := audit.Snapshot(rule.ID)
		if ShouldPrune(rule, stat, policy) {
			stat.Pruned = true
			trace.skip(rule.ID, ReasonAuditLowHitRate)
			trace.SavedTokens += ruleCost(rule)
			trace.addPrunedTokens(rule.Level, ruleCost(rule))
			continue
		}
		kept = append(kept, rule)
	}
	trace.Loaded = trace.Loaded[:0]
	trace.Levels = trace.Levels[:0]
	trace.LevelLoaded = map[string]int{}
	trace.LevelUsage = map[string]int{}
	trace.EstimatedUsed = 0
	for _, rule := range kept {
		trace.Loaded = append(trace.Loaded, rule.ID)
		trace.Levels = append(trace.Levels, string(rule.Level))
		trace.addLevelLoaded(rule.Level)
		trace.addLevelUsage(rule.Level, ruleCost(rule))
		trace.EstimatedUsed += ruleCost(rule)
	}
	return kept, trace
}
