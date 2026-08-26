package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/audit"
	goalDomain "agentcanvas/internal/domain/goal"
)

type WorkspaceContext struct {
	ID                 int64  `json:"workspace_id"`
	ProjectID          int64  `json:"project_id"`
	RunID              int64  `json:"run_id"`
	Kind               string `json:"kind"`
	RepositoryRoot     string `json:"repository_root"`
	WorkspacePath      string `json:"workspace_path"`
	BranchName         string `json:"branch_name"`
	BaseSHA            string `json:"base_sha,omitempty"`
	HeadSHA            string `json:"head_sha,omitempty"`
	Dirty              bool   `json:"dirty"`
	HasUnpushedCommits bool   `json:"has_unpushed_commits"`
	FileWriteEnabled   bool   `json:"file_write_enabled"`
	GitEnabled         bool   `json:"git_enabled"`
	ExecEnabled        bool   `json:"exec_enabled"`
}

type ToolRunContext struct {
	OwnerID         int64
	AgentID         int64
	RunID           int64
	Mode            string
	DelegationDepth int
	ConversationID  *int64
	ProjectID       int64
	// Task is the current run objective. Tools use it as a semantic query
	// when the model omits an explicit query (for example memory recall).
	Task                        string
	Workspace                   *WorkspaceContext
	EmitEvent                   func(context.Context, string, map[string]any) error
	GoalRepository              goalDomain.Repository
	GoalTokenBudgetCeiling      *int64
	DefaultModeRequestUserInput bool
}

type ToolResult struct {
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
	ContentText string          `json:"content_text,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Approval    *ToolApproval   `json:"approval,omitempty"`
}

// ToolApproval is a structured, user-facing approval request emitted by a
// tool after it has inspected its arguments. Unlike risk approvals, these
// requests can expose mutually exclusive choices (for example resolving a
// conflicting memory as keep-existing, replace, or keep-both).
type ToolApproval struct {
	Kind       string              `json:"kind"`
	Title      string              `json:"title"`
	Reason     string              `json:"reason"`
	IsBlocking bool                `json:"is_blocking,omitempty"`
	Options    []ApprovalOption    `json:"options,omitempty"`
	Questions  []UserInputQuestion `json:"questions,omitempty"`
}

type UserInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	Options  []UserInputOption `json:"options"`
	IsOther  bool              `json:"is_other,omitempty"`
}

type UserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type ApprovalOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

const (
	SideEffectNone           = "none"
	SideEffectRead           = "read"
	SideEffectWrite          = "write"
	SideEffectExternalAction = "external_action"
)

const (
	ExecutionSerial     = "serial"
	ExecutionDelegation = "delegation"
)

type ToolMetadata struct {
	RiskLevel        string   `json:"risk_level"`
	RequiresApproval bool     `json:"requires_approval"`
	TimeoutMS        int      `json:"timeout_ms,omitempty"`
	MaxOutputBytes   int      `json:"max_output_bytes,omitempty"`
	SideEffect       string   `json:"side_effect,omitempty"`
	AllowedHosts     []string `json:"allowed_hosts,omitempty"`
	ExecutionClass   string   `json:"execution_class,omitempty"`
}

func EffectiveLimit(value, ceiling int) int {
	if ceiling > 0 && (value <= 0 || ceiling < value) {
		return ceiling
	}
	return value
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if index := strings.IndexByte(host, '/'); index >= 0 {
		host = host[:index]
	}
	if index := strings.LastIndexByte(host, ':'); index > 0 {
		host = host[:index]
	}
	return host
}

func NormalizeHosts(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	result := make([]string, 0, len(hosts))
	for _, host := range hosts {
		normalized := NormalizeHost(host)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func ValidateAllowedHosts(toolHosts, policyHosts []string, denyAll bool) error {
	if denyAll && len(toolHosts) > 0 {
		return fmt.Errorf("tool hosts are denied by intersected policy allowlists")
	}
	allowed := make(map[string]struct{}, len(policyHosts))
	for _, host := range NormalizeHosts(policyHosts) {
		allowed[host] = struct{}{}
	}
	if len(allowed) == 0 || len(toolHosts) == 0 {
		return nil
	}
	for _, host := range toolHosts {
		normalized := NormalizeHost(host)
		if normalized == "" {
			continue
		}
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("tool host %s is not allowed by policy", normalized)
		}
	}
	return nil
}

type RuntimeTool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error)
}

// AuditedTool records workspace tool execution without persisting source file
// contents. workspace_exec intentionally retains its command for operator
// review; file bodies and patch text are replaced by length and SHA-256.
type AuditedTool struct {
	Tool   RuntimeTool
	Audits audit.Repository
}

func (t AuditedTool) Name() string                { return t.Tool.Name() }
func (t AuditedTool) Description() string         { return t.Tool.Description() }
func (t AuditedTool) Parameters() json.RawMessage { return t.Tool.Parameters() }
func (t AuditedTool) Metadata() ToolMetadata      { return MetadataOf(t.Tool) }
func (t AuditedTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	result, runErr := t.Tool.Execute(ctx, rc, input)
	if t.Audits == nil || rc.OwnerID <= 0 || rc.Workspace == nil {
		return result, runErr
	}
	detail := map[string]any{
		"agent_id": rc.AgentID, "run_id": rc.RunID, "tool": t.Tool.Name(),
		"repository_root": rc.Workspace.RepositoryRoot, "workspace_path": rc.Workspace.WorkspacePath, "branch_name": rc.Workspace.BranchName,
		"input": sanitizedAuditInput(input), "succeeded": runErr == nil,
	}
	if runErr != nil {
		detail["error_message"] = runErr.Error()
	}
	if result != nil {
		detail["result_summary"] = truncateAuditText(result.ContentText, 2048)
		if len(result.Metadata) > 0 {
			detail["metadata"] = result.Metadata
		}
	}
	_ = t.Audits.Create(ctx, audit.NewLog(rc.OwnerID, rc.OwnerID, "workspace.tool."+t.Tool.Name(), "workspace", strconv.FormatInt(rc.Workspace.ID, 10), detail, "", ""))
	return result, runErr
}

func sanitizedAuditInput(input json.RawMessage) map[string]any {
	var value map[string]any
	if err := json.Unmarshal(input, &value); err != nil {
		return map[string]any{"invalid_json": true}
	}
	for _, key := range []string{"content", "old_string", "new_string", "patch"} {
		raw, ok := value[key].(string)
		if !ok {
			continue
		}
		sum := sha256.Sum256([]byte(raw))
		value[key+"_chars"] = len([]rune(raw))
		value[key+"_sha256"] = hex.EncodeToString(sum[:])
		delete(value, key)
	}
	return value
}

func truncateAuditText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

type MetadataProvider interface {
	Metadata() ToolMetadata
}

func MetadataOf(tool RuntimeTool) ToolMetadata {
	if tool == nil {
		return ToolMetadata{
			RiskLevel:  RiskLow,
			SideEffect: SideEffectNone,
		}
	}
	if provider, ok := tool.(MetadataProvider); ok {
		metadata := provider.Metadata()
		if metadata.RiskLevel == "" {
			metadata.RiskLevel = RiskLow
		}
		if metadata.SideEffect == "" {
			metadata.SideEffect = SideEffectNone
		}
		if metadata.ExecutionClass == "" {
			metadata.ExecutionClass = ExecutionSerial
		}
		return metadata
	}
	return ToolMetadata{
		RiskLevel:      RiskLow,
		SideEffect:     SideEffectNone,
		ExecutionClass: ExecutionSerial,
	}
}

type Registry interface {
	LoadForAgent(ctx context.Context, ownerID int64, toolIDs []int64) ([]RuntimeTool, error)
}

func ResultFromValue(value any) (*ToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		ContentJSON: data,
		ContentText: string(data),
	}, nil
}
