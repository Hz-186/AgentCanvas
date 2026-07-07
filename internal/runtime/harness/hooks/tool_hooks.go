package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"agentcanvas/internal/runtime/toolruntime"
)

type ToolPolicy struct {
	RequireApprovalForRisk []string `json:"require_approval_for_risk,omitempty"`
	MaxToolTimeoutMS       int      `json:"max_tool_timeout_ms,omitempty"`
	MaxToolOutputBytes     int      `json:"max_tool_output_bytes,omitempty"`
	AllowedHosts           []string `json:"allowed_hosts,omitempty"`
}

// human approval request
type Approval struct {
	ToolCallID string                   `json:"tool_call_id"`
	ToolName   string                   `json:"tool_name"`
	RiskLevel  string                   `json:"risk_level"`
	Reason     string                   `json:"reason"`
	Metadata   toolruntime.ToolMetadata `json:"metadata"`
}

// the record of hook
type Trace struct {
	Stage      string `json:"stage"`
	Hook       string `json:"hook"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
	Compressed bool   `json:"compressed,omitempty"`
}

type PreToolUseRequest struct {
	ToolCallID string
	ToolName   string
	Arguments  json.RawMessage
	Metadata   toolruntime.ToolMetadata
	Policy     ToolPolicy
}

type PreToolUseResult struct {
	Context  context.Context
	Cancel   context.CancelFunc
	Approval *Approval
	Denied   error
	Traces   []Trace
}

type PostToolUseRequest struct {
	ToolName        string
	Content         string
	OutputJSON      json.RawMessage
	Metadata        toolruntime.ToolMetadata
	Policy          ToolPolicy
	SensitiveFields []string
}

type PostToolUseResult struct {
	Content    string
	OutputJSON json.RawMessage
	Compressed bool
	Traces     []Trace
}

type PreToolUseHook interface {
	BeforeToolUse(ctx context.Context, req PreToolUseRequest) PreToolUseResult
}

type PostToolUseHook interface {
	AfterToolUse(ctx context.Context, req PostToolUseRequest) PostToolUseResult
}

type ToolHookChain struct {
	Pre  []PreToolUseHook
	Post []PostToolUseHook
}

func (c ToolHookChain) Empty() bool {
	return len(c.Pre) == 0 && len(c.Post) == 0
}

func DefaultToolHookChain() ToolHookChain {
	return ToolHookChain{
		Pre:  []PreToolUseHook{PolicyPreToolUseHook{}},
		Post: []PostToolUseHook{ObservationPostToolUseHook{}},
	}
}

func (c ToolHookChain) BeforeToolUse(ctx context.Context, req PreToolUseRequest) PreToolUseResult {
	result := PreToolUseResult{Context: ctx}
	for _, hook := range c.Pre {
		if hook == nil {
			continue
		}
		next := hook.BeforeToolUse(result.Context, req)
		result.Traces = append(result.Traces, next.Traces...)
		if next.Context != nil {
			result.Context = next.Context
		}
		if next.Cancel != nil {
			result.Cancel = next.Cancel
		}
		if next.Approval != nil {
			result.Approval = next.Approval
			return result
		}
		if next.Denied != nil {
			result.Denied = next.Denied
			return result
		}
	}
	return result
}

func (c ToolHookChain) AfterToolUse(ctx context.Context, req PostToolUseRequest) PostToolUseResult {
	result := PostToolUseResult{Content: req.Content, OutputJSON: req.OutputJSON}
	current := req
	for _, hook := range c.Post {
		if hook == nil {
			continue
		}
		next := hook.AfterToolUse(ctx, current)
		result.Traces = append(result.Traces, next.Traces...)
		result.Content = next.Content
		result.OutputJSON = next.OutputJSON
		result.Compressed = result.Compressed || next.Compressed
		current.Content = result.Content
		current.OutputJSON = result.OutputJSON
	}
	return result
}

type PolicyPreToolUseHook struct{}

func (PolicyPreToolUseHook) BeforeToolUse(ctx context.Context, req PreToolUseRequest) PreToolUseResult {
	if reason := dangerousToolArgumentReason(req); reason != "" {
		err := fmt.Errorf("dangerous tool invocation blocked: %s", reason)
		return PreToolUseResult{Context: ctx, Denied: err, Traces: []Trace{{Stage: "pre_tool_use", Hook: "policy", Decision: "denied", Reason: err.Error()}}}
	}
	if approval := requiredApproval(req); approval != nil {
		return PreToolUseResult{Context: ctx, Approval: approval, Traces: []Trace{{Stage: "pre_tool_use", Hook: "policy", Decision: "approval_required", Reason: approval.Reason}}}
	}
	if err := validateAllowedHosts(req.Metadata.AllowedHosts, req.Policy.AllowedHosts); err != nil {
		return PreToolUseResult{Context: ctx, Denied: err, Traces: []Trace{{Stage: "pre_tool_use", Hook: "policy", Decision: "denied", Reason: err.Error()}}}
	}
	timeoutMS := effectiveTimeoutMS(req.Metadata, req.Policy)
	if timeoutMS <= 0 {
		return PreToolUseResult{Context: ctx, Traces: []Trace{{Stage: "pre_tool_use", Hook: "policy", Decision: "allowed"}}}
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	return PreToolUseResult{Context: execCtx, Cancel: cancel, Traces: []Trace{{Stage: "pre_tool_use", Hook: "policy", Decision: "allowed", Reason: fmt.Sprintf("timeout_ms=%d", timeoutMS)}}}
}

type ObservationPostToolUseHook struct{}

func (ObservationPostToolUseHook) AfterToolUse(ctx context.Context, req PostToolUseRequest) PostToolUseResult {
	_ = ctx
	fields := req.SensitiveFields
	if len(fields) == 0 {
		fields = DefaultSensitiveFields()
	}
	raw := RedactSensitiveFields(req.OutputJSON, fields)
	content := req.Content
	if len(raw) > 0 && string(raw) != string(req.OutputJSON) && strings.TrimSpace(content) == strings.TrimSpace(string(req.OutputJSON)) {
		content = string(raw)
	}
	maxBytes := effectiveMaxOutputBytes(req.Metadata, req.Policy)
	if maxBytes <= 0 {
		return PostToolUseResult{Content: content, OutputJSON: raw, Traces: []Trace{{Stage: "post_tool_use", Hook: "observation", Decision: "recorded"}}}
	}
	compactContent, contentCompressed := compactStringWithFlag(content, maxBytes)
	compactJSON, jsonCompressed := compactRawJSONWithFlag(raw, maxBytes)
	compressed := contentCompressed || jsonCompressed
	decision := "recorded"
	if compressed {
		decision = "compressed"
	}
	return PostToolUseResult{Content: compactContent, OutputJSON: compactJSON, Compressed: compressed, Traces: []Trace{{Stage: "post_tool_use", Hook: "observation", Decision: decision, Compressed: compressed}}}
}

func requiredApproval(req PreToolUseRequest) *Approval {
	risk := strings.TrimSpace(req.Metadata.RiskLevel)
	if risk == "" {
		risk = toolruntime.RiskLow
	}
	required := req.Metadata.RequiresApproval
	for _, item := range req.Policy.RequireApprovalForRisk {
		if strings.EqualFold(strings.TrimSpace(item), risk) {
			required = true
			break
		}
	}
	if !required {
		return nil
	}
	reason := fmt.Sprintf("tool %s requires human approval because risk level is %s", req.ToolName, risk)
	if strings.EqualFold(req.ToolName, "request_human_approval") {
		var args struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(req.Arguments, &args); err == nil {
			parts := make([]string, 0, 2)
			if strings.TrimSpace(args.Action) != "" {
				parts = append(parts, "action: "+strings.TrimSpace(args.Action))
			}
			if strings.TrimSpace(args.Reason) != "" {
				parts = append(parts, "reason: "+strings.TrimSpace(args.Reason))
			}
			if len(parts) > 0 {
				reason = strings.Join(parts, "; ")
			}
		}
	}
	return &Approval{ToolCallID: req.ToolCallID, ToolName: req.ToolName, RiskLevel: risk, Reason: reason, Metadata: req.Metadata}
}

func effectiveTimeoutMS(metadata toolruntime.ToolMetadata, policy ToolPolicy) int {
	timeoutMS := metadata.TimeoutMS
	if policy.MaxToolTimeoutMS > 0 && (timeoutMS <= 0 || policy.MaxToolTimeoutMS < timeoutMS) {
		timeoutMS = policy.MaxToolTimeoutMS
	}
	return timeoutMS
}

func effectiveMaxOutputBytes(metadata toolruntime.ToolMetadata, policy ToolPolicy) int {
	maxBytes := metadata.MaxOutputBytes
	if policy.MaxToolOutputBytes > 0 && (maxBytes <= 0 || policy.MaxToolOutputBytes < maxBytes) {
		maxBytes = policy.MaxToolOutputBytes
	}
	return maxBytes
}

func validateAllowedHosts(toolHosts []string, policyHosts []string) error {
	allowed := normalizedSet(policyHosts)
	if len(allowed) == 0 || len(toolHosts) == 0 {
		return nil
	}
	for _, host := range toolHosts {
		normalized := normalizeHost(host)
		if normalized == "" {
			continue
		}
		if !slices.Contains(allowed, normalized) {
			return fmt.Errorf("tool host %s is not allowed by policy", normalized)
		}
	}
	return nil
}

func dangerousToolArgumentReason(req PreToolUseRequest) string {
	toolName := strings.ToLower(strings.TrimSpace(req.ToolName))
	if toolName == "" || len(req.Arguments) == 0 {
		return ""
	}
	if !strings.Contains(toolName, "shell") && !strings.Contains(toolName, "sandbox") && !strings.Contains(toolName, "python") && !strings.Contains(toolName, "execute") {
		return ""
	}
	payload := strings.ToLower(string(req.Arguments))
	payload = strings.Join(strings.Fields(payload), " ")
	if payload == "" {
		return ""
	}
	dangerousPatterns := []string{
		"rm -rf /",
		"rm -fr /",
		"rm -rf /*",
		"rm -fr /*",
		"sudo rm -rf",
		"mkfs.",
		"mkfs ",
		"mkswap ",
		"dd if=",
		"dd ",
		" of=/dev/",
		":(){",
		": () {",
		"fork bomb",
		"/dev/sda",
		"/dev/disk",
		"/dev/nvme",
		"curl ",
		"| sh",
		"| bash",
		"bash <(",
		"sh <(",
		"wget ",
		"chmod -r 777 /",
		"chown -r ",
		"> /etc/",
		"tee /etc/",
		"launchctl unload",
		"systemctl disable",
		"iptables -f",
		"pfctl -d",
		"bash -c",
		"sh -c",
		"network_enabled\":true",
		"network_enabled\": true",
	}
	for _, pattern := range dangerousPatterns {
		if strings.Contains(payload, pattern) {
			return pattern
		}
	}
	return ""
}

func normalizedSet(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		normalized := normalizeHost(host)
		if normalized == "" || slices.Contains(out, normalized) {
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.LastIndexByte(host, ':'); idx > 0 {
		host = host[:idx]
	}
	return host
}

func DefaultSensitiveFields() []string {
	return []string{"api_key", "apikey", "authorization", "access_token", "refresh_token", "token", "password", "secret"}
}

func RedactSensitiveFields(raw json.RawMessage, fields []string) json.RawMessage {
	if len(fields) == 0 || len(raw) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	for _, field := range fields {
		if _, ok := m[field]; ok {
			m[field] = "[REDACTED]"
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return json.RawMessage(out)
}

func compactStringWithFlag(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	return value[:maxBytes] + "...[truncated]", true
}

func compactRawJSONWithFlag(raw json.RawMessage, maxBytes int) (json.RawMessage, bool) {
	if len(raw) <= maxBytes {
		return raw, false
	}
	return json.RawMessage(strconvQuote(compactString(string(raw), maxBytes))), true
}

func compactString(value string, maxBytes int) string {
	compact, _ := compactStringWithFlag(value, maxBytes)
	return compact
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
