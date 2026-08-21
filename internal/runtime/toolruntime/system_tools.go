package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	gitinfra "agentcanvas/internal/infrastructure/git"
)

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
