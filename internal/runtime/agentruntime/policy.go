package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type contextPolicy struct {
	MaxInputChars                   int               `json:"max_input_chars"`
	MaxInputTokens                  int               `json:"max_input_tokens"`
	ContextWindowTokens             int               `json:"context_window_tokens"`
	ReservedOutputTokens            int               `json:"reserved_output_tokens"`
	ContextSafetyMarginTokens       int               `json:"context_safety_margin_tokens"`
	MaxRuleTokens                   int               `json:"max_rule_tokens"`
	ModelAutoCompactTokenLimit      int               `json:"model_auto_compact_token_limit"`
	ModelAutoCompactTokenLimitScope string            `json:"model_auto_compact_token_limit_scope"`
	CompactionProviderID            int64             `json:"compaction_provider_id"`
	CompactionModel                 string            `json:"compaction_model"`
	CompactionMode                  string            `json:"compaction_mode"`
	RetainClientDeveloperMessages   bool              `json:"retain_client_developer_messages"`
	CompactPrompt                   string            `json:"compact_prompt"`
	Retrieval                       retrievalPolicy   `json:"retrieval"`
	DeprecatedRules                 []json.RawMessage `json:"rules"`
}

type retrievalPolicy struct {
	Enabled             *bool  `json:"enabled"`
	Mode                string `json:"mode"`
	EmbeddingProviderID int64  `json:"embedding_provider_id"`
	EmbeddingModel      string `json:"embedding_model"`
	EmbeddingDimensions int    `json:"embedding_dimensions"`
	CandidateK          int    `json:"candidate_k"`
	RRFK                int    `json:"rrf_k"`
	QueryRewriteMode    string `json:"query_rewrite_mode"`
	MaxRewrites         int    `json:"max_rewrites"`
	MaxSubqueries       int    `json:"max_subqueries"`
}

func decodeContextPolicy(raw json.RawMessage) (contextPolicy, error) {
	var policy contextPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return policy, err
	}
	return policy, nil
}

type toolPolicyOverride struct {
	RequireApprovalForRisk *[]string `json:"require_approval_for_risk"`
	MaxToolTimeoutMS       *int      `json:"max_tool_timeout_ms"`
	MaxToolOutputBytes     *int      `json:"max_tool_output_bytes"`
	AllowedHosts           *[]string `json:"allowed_hosts"`
	DenyAllHosts           *bool     `json:"deny_all_hosts"`
}

func applyRuntimeContextPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	policy, err := decodeContextPolicy(raw)
	if err != nil {
		return cfg
	}
	if policy.MaxInputChars > 0 {
		cfg.MaxInputChars = policy.MaxInputChars
	} else if policy.MaxInputTokens > 0 {
		cfg.MaxInputTokens = policy.MaxInputTokens
		cfg.MaxInputChars = policy.MaxInputTokens * 4
	}
	applyContextPolicy(&cfg, policy)
	return cfg
}

func applyContextPolicy(cfg *agentRuntimeConfig, policy contextPolicy) {
	cfg.RetrievalPolicy = policy.Retrieval
	if policy.ContextWindowTokens > 0 {
		cfg.ContextWindowTokens = policy.ContextWindowTokens
	}
	if policy.ReservedOutputTokens > 0 {
		cfg.ReservedOutputTokens = policy.ReservedOutputTokens
	}
	if policy.ContextSafetyMarginTokens > 0 {
		cfg.ContextSafetyMarginTokens = policy.ContextSafetyMarginTokens
	}
	if policy.MaxRuleTokens > 0 {
		cfg.MaxRuleTokens = policy.MaxRuleTokens
	}
	if policy.ModelAutoCompactTokenLimit > 0 {
		cfg.ModelAutoCompactTokenLimit = policy.ModelAutoCompactTokenLimit
	}
	if strings.TrimSpace(policy.ModelAutoCompactTokenLimitScope) != "" {
		cfg.ModelAutoCompactTokenLimitScope = strings.TrimSpace(policy.ModelAutoCompactTokenLimitScope)
	}
	if policy.CompactionProviderID > 0 {
		cfg.CompactionProviderID = policy.CompactionProviderID
	}
	if strings.TrimSpace(policy.CompactionModel) != "" {
		cfg.CompactionModel = strings.TrimSpace(policy.CompactionModel)
	}
	if strings.TrimSpace(policy.CompactionMode) != "" {
		cfg.CompactionMode = strings.TrimSpace(policy.CompactionMode)
	}
	cfg.RetainClientDeveloperMessages = policy.RetainClientDeveloperMessages
	if strings.TrimSpace(policy.CompactPrompt) != "" {
		cfg.CompactPrompt = strings.TrimSpace(policy.CompactPrompt)
	}
}

func applyRuntimeToolPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	var policy toolPolicyOverride
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg
	}
	var approvals, hosts []string
	var timeoutMS, outputBytes int
	var denyAll bool
	if policy.RequireApprovalForRisk != nil {
		approvals = *policy.RequireApprovalForRisk
	}
	if policy.MaxToolTimeoutMS != nil {
		timeoutMS = *policy.MaxToolTimeoutMS
	}
	if policy.MaxToolOutputBytes != nil {
		outputBytes = *policy.MaxToolOutputBytes
	}
	if policy.AllowedHosts != nil {
		hosts = *policy.AllowedHosts
	}
	if policy.DenyAllHosts != nil {
		denyAll = *policy.DenyAllHosts
	}
	mergeAgentToolPolicy(&cfg, approvals, timeoutMS, outputBytes, hosts, denyAll)
	return cfg
}

func mergeAgentToolPolicy(cfg *agentRuntimeConfig, approvals []string, timeoutMS, outputBytes int, hosts []string, denyAll bool) {
	if cfg == nil {
		return
	}
	for _, risk := range approvals {
		risk = strings.TrimSpace(risk)
		if risk == "" {
			continue
		}
		found := false
		for _, existing := range cfg.RequireApprovalForRisk {
			if strings.EqualFold(existing, risk) {
				found = true
				break
			}
		}
		if !found {
			cfg.RequireApprovalForRisk = append(cfg.RequireApprovalForRisk, risk)
		}
	}
	if timeoutMS > 0 && (cfg.MaxToolTimeoutMS <= 0 || timeoutMS < cfg.MaxToolTimeoutMS) {
		cfg.MaxToolTimeoutMS = timeoutMS
	}
	if outputBytes > 0 && (cfg.MaxToolOutputBytes <= 0 || outputBytes < cfg.MaxToolOutputBytes) {
		cfg.MaxToolOutputBytes = outputBytes
	}
	if denyAll {
		cfg.DenyAllHosts = true
	}
	if len(hosts) == 0 || cfg.DenyAllHosts {
		return
	}
	constraint := toolruntime.NormalizeHosts(hosts)
	if len(cfg.AllowedHosts) == 0 {
		cfg.AllowedHosts = constraint
		return
	}
	current := toolruntime.NormalizeHosts(cfg.AllowedHosts)
	intersection := make([]string, 0, len(current))
	for _, host := range current {
		for _, allowed := range constraint {
			if host == allowed {
				intersection = append(intersection, host)
				break
			}
		}
	}
	if len(intersection) == 0 {
		cfg.AllowedHosts = nil
		cfg.DenyAllHosts = true
		return
	}
	cfg.AllowedHosts = intersection
}

func applyRuntimeMemoryPolicy(cfg agentRuntimeConfig, raw json.RawMessage) agentRuntimeConfig {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return cfg
	}
	policy, err := memory.ParsePolicy(raw)
	if err != nil {
		return cfg
	}
	cfg.MemoryPolicy = policy
	if policy.Enabled != nil {
		cfg.MemoryEnabled = *policy.Enabled
		cfg.MemoryEnabledSet = true
	}
	return cfg
}

func validateAgentToolPolicyJSON(raw json.RawMessage) error {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var policy toolPolicyOverride
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("%w: agent runtime tool_policy_json is invalid", agenterrors.ErrInvalidInput)
	}
	if policy.MaxToolTimeoutMS != nil && (*policy.MaxToolTimeoutMS < 0 || *policy.MaxToolTimeoutMS > 10*60*1000) {
		return fmt.Errorf("%w: agent runtime max_tool_timeout_ms must be <= 600000", agenterrors.ErrInvalidInput)
	}
	if policy.MaxToolOutputBytes != nil && (*policy.MaxToolOutputBytes < 0 || *policy.MaxToolOutputBytes > 2*1024*1024) {
		return fmt.Errorf("%w: agent runtime max_tool_output_bytes must be <= 2097152", agenterrors.ErrInvalidInput)
	}
	if policy.RequireApprovalForRisk != nil {
		for _, risk := range *policy.RequireApprovalForRisk {
			normalized := strings.TrimSpace(risk)
			if normalized != "" && normalized != toolruntime.RiskLow && normalized != toolruntime.RiskMedium && normalized != toolruntime.RiskHigh {
				return fmt.Errorf("%w: agent runtime require_approval_for_risk contains unsupported risk level", agenterrors.ErrInvalidInput)
			}
		}
	}
	return nil
}

func validateAgentMemoryPolicyJSON(raw json.RawMessage) error {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	if _, err := memory.ParsePolicy(raw); err != nil {
		return fmt.Errorf("%w: agent runtime memory_policy_json is invalid", agenterrors.ErrInvalidInput)
	}
	return nil
}

func validateAgentContextPolicyJSON(raw json.RawMessage) error {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	policy, err := decodeContextPolicy(raw)
	if err != nil {
		return fmt.Errorf("%w: agent runtime context_policy_json is invalid", agenterrors.ErrInvalidInput)
	}
	if policy.MaxInputChars < 0 || policy.MaxInputTokens < 0 || policy.ContextWindowTokens < 0 || policy.ReservedOutputTokens < 0 || policy.ContextSafetyMarginTokens < 0 || policy.MaxRuleTokens < 0 || policy.ModelAutoCompactTokenLimit < 0 || policy.CompactionProviderID < 0 {
		return fmt.Errorf("%w: agent runtime context policy limits must be positive", agenterrors.ErrInvalidInput)
	}
	if scope := strings.TrimSpace(policy.ModelAutoCompactTokenLimitScope); scope != "" && scope != "total" && scope != "body_after_prefix" {
		return fmt.Errorf("%w: agent runtime model_auto_compact_token_limit_scope must be total or body_after_prefix", agenterrors.ErrInvalidInput)
	}
	if mode := strings.TrimSpace(policy.CompactionMode); mode != "" && mode != "summary" && mode != "token_budget" {
		return fmt.Errorf("%w: agent runtime compaction_mode must be summary or token_budget", agenterrors.ErrInvalidInput)
	}
	if len(policy.DeprecatedRules) > 0 {
		return fmt.Errorf("%w: agent runtime context_policy_json.rules is not supported; use Agent definition rules", agenterrors.ErrInvalidInput)
	}
	return nil
}
