package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/domain"
	goalDomain "agentcanvas/internal/domain/goal"
	gitinfra "agentcanvas/internal/infrastructure/git"
	runtimeevent "agentcanvas/internal/runtime/event"
)

const updatePlanModeError = "update_plan is a TODO/checklist tool and is not allowed in Plan mode"

type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type UpdatePlanInput struct {
	Explanation string     `json:"explanation,omitempty"`
	Plan        []PlanItem `json:"plan"`
}

type updatePlanArguments struct {
	Explanation string      `json:"explanation,omitempty"`
	Plan        *[]PlanItem `json:"plan"`
}

type UpdatePlanTool struct{}

func (UpdatePlanTool) Name() string { return "update_plan" }

func (UpdatePlanTool) Description() string {
	return "Updates the task plan. Provide an optional explanation and a list of plan items with pending, in_progress, or completed status."
}

type RequestUserInputTool struct{}

func (RequestUserInputTool) Name() string { return "request_user_input" }
func (RequestUserInputTool) Description() string {
	return "Request user input for one to three short questions and wait for the response. This tool is available in Plan mode and, when enabled, Default mode."
}
func (RequestUserInputTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","description":"Questions to show the user. Prefer 1 and do not exceed 3","items":{"type":"object","properties":{"id":{"type":"string","description":"Stable identifier for mapping answers (snake_case)."},"header":{"type":"string","description":"Short header label shown in the UI (12 or fewer chars)."},"question":{"type":"string","description":"Single-sentence prompt shown to the user."},"options":{"type":"array","description":"Provide 2-3 mutually exclusive choices. Put the recommended option first.","items":{"type":"object","properties":{"label":{"type":"string","description":"User-facing label (1-5 words)."},"description":{"type":"string","description":"One short sentence explaining impact/tradeoff if selected."}},"required":["label","description"],"additionalProperties":false}}},"required":["id","header","question","options"],"additionalProperties":false}}},"required":["questions"],"additionalProperties":false}`)
}
func (RequestUserInputTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectNone}
}
func (RequestUserInputTool) Execute(_ context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if strings.TrimSpace(rc.Mode) != "plan" && !(strings.TrimSpace(rc.Mode) == "default" && rc.DefaultModeRequestUserInput) {
		return &ToolResult{ContentText: "request_user_input is unavailable in Default mode", IsError: true}, errors.New("request_user_input is unavailable in Default mode")
	}
	if rc.DelegationDepth > 0 {
		return &ToolResult{ContentText: "request_user_input is unavailable in subagent mode", IsError: true}, errors.New("request_user_input is unavailable in subagent mode")
	}
	var args struct {
		Questions []UserInputQuestion `json:"questions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("invalid arguments: trailing JSON value")
		}
		return nil, err
	}
	for index := range args.Questions {
		question := &args.Questions[index]
		if len(question.Options) == 0 {
			return nil, errors.New("request_user_input requires non-empty options for every question")
		}
		question.IsOther = true
	}
	return &ToolResult{ContentText: "Waiting for user input", Approval: &ToolApproval{Kind: "request_user_input", Title: "User input required", Reason: "The agent needs your answer before continuing.", IsBlocking: strings.TrimSpace(rc.Mode) == "plan", Questions: args.Questions}}, nil
}

func (UpdatePlanTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"explanation":{"type":"string"},"plan":{"type":"array","items":{"type":"object","properties":{"step":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["step","status"],"additionalProperties":false}}},"required":["plan"],"additionalProperties":false}`)
}

func (UpdatePlanTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectNone}
}

func (UpdatePlanTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.Mode == "plan" {
		return &ToolResult{ContentText: updatePlanModeError, IsError: true}, errors.New(updatePlanModeError)
	}
	var wire updatePlanArguments
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid arguments: trailing JSON value")
	}
	if wire.Plan == nil {
		return nil, errors.New("invalid arguments: plan is required")
	}
	args := UpdatePlanInput{Explanation: wire.Explanation, Plan: *wire.Plan}
	for _, item := range args.Plan {
		if item.Step == "" {
			return nil, errors.New("invalid arguments: plan step is required")
		}
		switch item.Status {
		case "pending", "in_progress", "completed":
		default:
			return nil, fmt.Errorf("invalid arguments: unsupported plan status %q", item.Status)
		}
	}
	payload := map[string]any{"run_id": rc.RunID, "plan": args.Plan}
	if rc.ConversationID != nil {
		payload["conversation_id"] = *rc.ConversationID
	}
	if args.Explanation != "" {
		payload["explanation"] = args.Explanation
	}
	if rc.EmitEvent != nil {
		if err := rc.EmitEvent(ctx, runtimeevent.TodoUpdated, payload); err != nil {
			return nil, err
		}
	}
	return &ToolResult{ContentText: "Plan updated"}, nil
}

type GetGoalTool struct{}

func (GetGoalTool) Name() string { return "get_goal" }
func (GetGoalTool) Description() string {
	return "Read the current thread goal, status, and token usage."
}
func (GetGoalTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (GetGoalTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}
func (GetGoalTool) Execute(ctx context.Context, rc ToolRunContext, _ json.RawMessage) (*ToolResult, error) {
	if rc.GoalRepository == nil || rc.ConversationID == nil {
		return nil, errors.New("goal repository is unavailable")
	}
	item, err := rc.GoalRepository.Get(ctx, rc.OwnerID, *rc.ConversationID)
	if errors.Is(err, goalDomain.ErrNotFound) {
		item = nil
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return goalResult(item), nil
}

type CreateGoalTool struct{}

func (CreateGoalTool) Name() string        { return "create_goal" }
func (CreateGoalTool) Description() string { return "Create a persistent active goal for this thread." }
func (CreateGoalTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"objective":{"type":"string"},"token_budget":{"type":"integer","minimum":1}},"required":["objective"],"additionalProperties":false}`)
}
func (CreateGoalTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectWrite}
}
func (CreateGoalTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.GoalRepository == nil || rc.ConversationID == nil {
		return nil, errors.New("goal repository is unavailable")
	}
	var args struct {
		Objective   string `json:"objective"`
		TokenBudget *int64 `json:"token_budget"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("invalid arguments: trailing JSON value")
		}
		return nil, err
	}
	var normalizeErr error
	args.Objective, normalizeErr = goalDomain.NormalizeObjective(args.Objective)
	if normalizeErr != nil {
		return nil, normalizeErr
	}
	if args.TokenBudget != nil && *args.TokenBudget <= 0 {
		return nil, errors.New("goal token budget must be positive")
	}
	if existing, err := rc.GoalRepository.Get(ctx, rc.OwnerID, *rc.ConversationID); err == nil && existing != nil && existing.Status != goalDomain.StatusComplete {
		return nil, errors.New("cannot create a new goal because this thread has an unfinished goal")
	}
	budget, err := goalDomain.NormalizeBudget(args.TokenBudget, rc.GoalTokenBudgetCeiling)
	if err != nil {
		return nil, err
	}
	item := &goalDomain.ThreadGoal{BaseModel: domain.BaseModel{OwnerID: rc.OwnerID}, ConversationID: *rc.ConversationID, Objective: args.Objective, Status: goalDomain.StatusActive, TokenBudget: budget}
	if err := rc.GoalRepository.Create(ctx, item); err != nil {
		return nil, err
	}
	emitGoalEvent(ctx, rc, item)
	return goalResult(item), nil
}

type UpdateGoalTool struct{}

func (UpdateGoalTool) Name() string { return "update_goal" }
func (UpdateGoalTool) Description() string {
	return "Mark the existing goal complete or blocked; pause/resume is controlled by the user or system."
}
func (UpdateGoalTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["complete","blocked"]}},"required":["status"],"additionalProperties":false}`)
}
func (UpdateGoalTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectWrite}
}
func (UpdateGoalTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.GoalRepository == nil || rc.ConversationID == nil {
		return nil, errors.New("goal repository is unavailable")
	}
	var args struct {
		Status string `json:"status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("invalid arguments: trailing JSON value")
		}
		return nil, err
	}
	if args.Status != goalDomain.StatusComplete && args.Status != goalDomain.StatusBlocked {
		return nil, errors.New("update_goal can only mark the existing goal complete or blocked; pause, resume, budget-limited, and usage-limited status changes are controlled by the user or system")
	}
	item, err := rc.GoalRepository.Get(ctx, rc.OwnerID, *rc.ConversationID)
	if err != nil {
		return nil, errors.New("cannot update goal because this thread has no goal")
	}
	if !goalDomain.CanSetStatus(item.Status, args.Status) {
		return nil, errors.New("cannot change a terminal goal status")
	}
	item.Status = args.Status
	if err := rc.GoalRepository.Update(ctx, item, item.GoalID); err != nil {
		return nil, err
	}
	emitGoalEvent(ctx, rc, item)
	return goalResult(item), nil
}

func goalResult(item *goalDomain.ThreadGoal) *ToolResult {
	if item == nil {
		result, _ := ResultFromValue(map[string]any{"goal": nil, "remaining_tokens": nil, "completion_budget_report": nil})
		return result
	}
	remaining := any(nil)
	if item.TokenBudget != nil {
		remaining = maxInt64(*item.TokenBudget-item.TokensUsed, 0)
	}
	value := map[string]any{"goal": item, "remaining_tokens": remaining, "completion_budget_report": nil}
	if item.Status == goalDomain.StatusComplete {
		value["completion_budget_report"] = completionBudgetReport(item)
	}
	result, _ := ResultFromValue(value)
	return result
}

func completionBudgetReport(item *goalDomain.ThreadGoal) string {
	if item == nil || (item.TokenBudget == nil && item.TimeUsedSeconds <= 0) {
		return ""
	}
	return "Goal achieved. Report final usage from this tool result's structured goal fields. If token_budget is present, include token usage from tokens_used and token_budget. If time_used_seconds is greater than 0, summarize elapsed time in a concise, human-friendly form appropriate to the response language."
}

func emitGoalEvent(ctx context.Context, rc ToolRunContext, item *goalDomain.ThreadGoal) {
	if rc.EmitEvent != nil {
		_ = rc.EmitEvent(ctx, runtimeevent.GoalUpdated, map[string]any{"goal": item})
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

type HumanApprovalTool struct{}

func (HumanApprovalTool) Name() string { return "request_human_approval" }

func (HumanApprovalTool) Description() string {
	return "Request human approval before executing a sensitive action. Use this when the action requires human review."
}

func (HumanApprovalTool) Parameters() json.RawMessage {
	return json.RawMessage(
		`{"type":"object","properties":{"action":{"type":"string","description":"the action requiring approval"},"reason":{"type":"string","description":"why approval is needed"}},"required":["action","reason"]}`,
	)
}

func (HumanApprovalTool) Metadata() ToolMetadata {
	return ToolMetadata{
		RiskLevel:        RiskHigh,
		RequiresApproval: true,
		SideEffect:       SideEffectExternalAction,
	}
}

func (HumanApprovalTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	var args struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return &ToolResult{
		ContentText: "Human approval requested for action: " + args.Action + ". Message: " + args.Reason + ". Waiting for approval before proceeding.",
		IsError:     false,
	}, nil
}

type WorkspaceExecTool struct {
	MaxOutputBytes int
	Timeout        time.Duration
}

func (WorkspaceExecTool) Name() string { return "workspace_exec" }
func (WorkspaceExecTool) Description() string {
	return "Run a non-interactive shell command with cwd fixed to the active Agent workspace."
}
func (WorkspaceExecTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout_ms":{"type":"number"}},"required":["command"],"additionalProperties":false}`)
}
func (WorkspaceExecTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskHigh, RequiresApproval: true, SideEffect: SideEffectExternalAction, TimeoutMS: 30000, MaxOutputBytes: 256 * 1024}
}
func (t WorkspaceExecTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.Workspace == nil || !rc.Workspace.ExecEnabled {
		return nil, errors.New("workspace execution is disabled")
	}
	var in struct {
		Command   string `json:"command"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New("command is required")
	}
	if err := validateWorkspaceCommand(in.Command, rc.Workspace.WorkspacePath); err != nil {
		return nil, err
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if in.TimeoutMS > 0 && time.Duration(in.TimeoutMS)*time.Millisecond < timeout {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := execCommand(commandCtx, in.Command, rc.Workspace.WorkspacePath)
	output := &cappedCommandOutput{max: t.outputLimit()}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			exitCode = -1
		}
	}
	value := map[string]any{"command": in.Command, "cwd": rc.Workspace.WorkspacePath, "output": output.String(), "exit_code": exitCode, "truncated": output.truncated}
	if err != nil {
		value["error"] = err.Error()
	}
	result, marshalErr := ResultFromValue(value)
	if marshalErr != nil {
		return nil, marshalErr
	}
	result.IsError = err != nil
	return result, err
}

type cappedCommandOutput struct {
	mu        sync.Mutex
	data      []byte
	max       int
	truncated bool
}

func (w *cappedCommandOutput) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(value)
	remaining := w.max - len(w.data)
	if remaining <= 0 {
		w.truncated = w.truncated || written > 0
		return written, nil
	}
	if len(value) > remaining {
		w.data = append(w.data, value[:remaining]...)
		w.truncated = true
		return written, nil
	}
	w.data = append(w.data, value...)
	return written, nil
}

func (w *cappedCommandOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.data)
}

func (t WorkspaceExecTool) outputLimit() int {
	if t.MaxOutputBytes <= 0 {
		return 256 * 1024
	}
	return t.MaxOutputBytes
}

func execCommand(ctx context.Context, command, cwd string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = cwd
	allowed := map[string]bool{
		"CI": true, "CLICOLOR": true, "CLICOLOR_FORCE": true, "COLORTERM": true,
		"DEVELOPER_DIR": true, "NO_COLOR": true, "PATH": true, "SDKROOT": true,
		"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "TZ": true,
	}
	environment := make([]string, 0, len(allowed)+8)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if allowed[name] || strings.HasPrefix(name, "LC_") {
			environment = append(environment, value)
		}
	}
	cmd.Env = append(environment, "HOME="+cwd, "PWD="+cwd, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "LC_ALL=en_US.UTF-8", "LANG=en_US.UTF-8")
	return cmd
}

var commandAbsolutePathPattern = regexp.MustCompile(`(?:^|[\s'"(=,:])(/[^\s'"();|&<>]*)`)
var commandExternalURLPattern = regexp.MustCompile(`(?:https?|ssh|git)://[^\s'"();|&<>]*`)

func validateWorkspaceCommand(command, workspace string) error {
	if strings.IndexByte(command, 0) >= 0 {
		return errors.New("command contains NUL")
	}
	lower := strings.ToLower(command)
	for _, forbidden := range []string{"cd ", "cd\t", "pushd ", "popd ", "git -c", "git -c ", "git --git-dir", "git --work-tree", " -c core.worktree", "../"} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("command would bypass the active workspace: %s", forbidden)
		}
	}
	workspace, _ = filepath.Abs(filepath.Clean(workspace))
	pathScan := commandExternalURLPattern.ReplaceAllString(command, "")
	for _, match := range commandAbsolutePathPattern.FindAllStringSubmatch(pathScan, -1) {
		candidate := strings.TrimRight(match[1], ",:")
		if candidate == "" {
			continue
		}
		if _, err := gitinfra.EnsureSafePath(workspace, candidate); err != nil {
			return errors.New("absolute command paths must remain inside the active workspace")
		}
	}
	for _, token := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';' || r == '|' || r == '&' || r == '(' || r == ')'
	}) {
		token = strings.Trim(token, "\"'`<>=,")
		if strings.HasPrefix(token, "/") && token != workspace && !strings.HasPrefix(token, workspace+string(filepath.Separator)) {
			return errors.New("absolute command paths must remain inside the active workspace")
		}
	}
	return nil
}
