package toolruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	workspacedomain "agentcanvas/internal/domain/workspace"
)

type WorkspaceExecution struct {
	Workspace      *workspacedomain.Workspace
	RepositoryRoot string
	Lease          *workspacedomain.RunLease
	manager        *WorkspaceManager
	stop           chan struct{}
	stopOnce       sync.Once
}

type WorkspaceManager struct {
	Repository    workspacedomain.Repository
	LeaseDuration time.Duration
	WorktreeRoot  string
}

func NewWorkspaceManager(repository workspacedomain.Repository) *WorkspaceManager {
	return &WorkspaceManager{Repository: repository, LeaseDuration: 2 * time.Minute, WorktreeRoot: filepath.Join(os.TempDir(), "agentcanvas-worktrees")}
}

func (m *WorkspaceManager) Acquire(ctx context.Context, ownerID, runID int64, item *workspacedomain.Workspace) (*WorkspaceExecution, error) {
	if m == nil || m.Repository == nil || item == nil || ownerID <= 0 || runID <= 0 {
		return nil, fmt.Errorf("workspace lease manager is not configured")
	}
	duration := m.LeaseDuration
	if duration <= 0 {
		duration = 2 * time.Minute
	}
	root := strings.TrimSpace(m.WorktreeRoot)
	if root == "" {
		root = filepath.Join(os.TempDir(), "agentcanvas-worktrees")
	}
	worktreePath := filepath.Join(root, fmt.Sprintf("workspace-%d", item.ID), fmt.Sprintf("run-%d", runID))
	branch := fmt.Sprintf("agentcanvas/run-%d", runID)
	if err := ensureGitWorktree(ctx, item.RootPath, worktreePath, branch); err != nil {
		return nil, err
	}
	token, err := randomLeaseToken()
	if err != nil {
		return nil, err
	}
	lease, err := m.Repository.AcquireRunLease(ctx, &workspacedomain.RunLease{OwnerID: ownerID, WorkspaceID: item.ID, RunID: runID, WorktreePath: worktreePath, LeaseToken: token, LeaseExpiresAt: time.Now().UTC().Add(duration), Status: workspacedomain.StatusActive})
	if err != nil {
		return nil, err
	}
	worktree := *item
	worktree.RootPath = lease.WorktreePath
	execution := &WorkspaceExecution{Workspace: &worktree, RepositoryRoot: item.RootPath, Lease: lease, manager: m, stop: make(chan struct{})}
	go execution.heartbeat(duration)
	return execution, nil
}

func (e *WorkspaceExecution) heartbeat(duration time.Duration) {
	interval := duration / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			ok, _ := e.manager.Repository.HeartbeatRunLease(ctx, e.Lease.ID, e.Lease.LeaseToken, time.Now().UTC().Add(duration))
			cancel()
			if !ok {
				return
			}
		}
	}
}

func (e *WorkspaceExecution) Suspend() {
	if e != nil {
		e.stopOnce.Do(func() {
			close(e.stop)
			if e.manager != nil && e.manager.Repository != nil && e.Lease != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, _ = e.manager.Repository.HeartbeatRunLease(ctx, e.Lease.ID, e.Lease.LeaseToken, time.Now().UTC().Add(24*time.Hour))
				cancel()
			}
		})
	}
}

func (e *WorkspaceExecution) Release(ctx context.Context) error {
	if e == nil || e.manager == nil || e.Lease == nil {
		return nil
	}
	e.Suspend()
	dirty, err := gitWorktreeDirty(ctx, e.Lease.WorktreePath)
	if err != nil {
		return err
	}
	if !dirty {
		if err := removeGitWorktree(ctx, e.RepositoryRoot, e.Lease.WorktreePath); err != nil {
			return err
		}
	}
	_, err = e.manager.Repository.ReleaseRunLease(ctx, e.Lease.ID, e.Lease.LeaseToken)
	return err
}

func (m *WorkspaceManager) CleanupExpired(ctx context.Context, before time.Time, limit int) error {
	items, err := m.Repository.ListExpiredRunLeases(ctx, before, limit)
	if err != nil {
		return err
	}
	for i := range items {
		item := &items[i]
		workspaceItem, findErr := m.Repository.FindWorkspace(ctx, item.OwnerID, item.WorkspaceID)
		if findErr == nil {
			_ = removeGitWorktree(ctx, workspaceItem.RootPath, item.WorktreePath)
		}
		_, _ = m.Repository.ReleaseRunLease(ctx, item.ID, item.LeaseToken)
	}
	return nil
}

func ensureGitWorktree(ctx context.Context, repositoryRoot, worktreePath, branch string) error {
	if _, err := os.Stat(worktreePath); err == nil {
		return runGit(ctx, "-C", worktreePath, "rev-parse", "--is-inside-work-tree")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := runGit(ctx, "-C", repositoryRoot, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("workspace root must be a git repository: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0750); err != nil {
		return err
	}
	if err := runGit(ctx, "-C", repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return runGit(ctx, "-C", repositoryRoot, "worktree", "add", worktreePath, branch)
	}
	return runGit(ctx, "-C", repositoryRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD")
}

func removeGitWorktree(ctx context.Context, worktreeRoot, worktreePath string) error {
	repositoryRoot := worktreeRoot
	if filepath.Clean(worktreeRoot) == filepath.Clean(worktreePath) {
		return nil
	}
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil
	}
	return runGit(ctx, "-C", repositoryRoot, "worktree", "remove", "--force", worktreePath)
}

func runGit(ctx context.Context, arguments ...string) error {
	command := exec.CommandContext(ctx, "git", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitWorktreeDirty(ctx context.Context, worktreePath string) (bool, error) {
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return false, nil
	}
	command := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain")
	output, err := command.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect worktree status: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func randomLeaseToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

type WorkspaceCommandRunner interface {
	Run(context.Context, *workspacedomain.Workspace, *workspacedomain.Pack, []string) (string, error)
}
type DockerWorkspaceCommandRunner struct{}

func (DockerWorkspaceCommandRunner) Run(ctx context.Context, workspace *workspacedomain.Workspace, pack *workspacedomain.Pack, command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("command is required")
	}
	if !slices.Contains(pack.CommandAllowlist, filepath.Base(command[0])) {
		return "", fmt.Errorf("command %q is outside workspace pack allowlist", command[0])
	}
	if pack.NetworkEnabled {
		return "", fmt.Errorf("network-enabled workspace execution requires an egress proxy")
	}
	timeout := time.Duration(pack.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"run", "--rm", "--network", "none", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=256m", "--tmpfs", "/workspace:rw,noexec,nosuid,size=64m", "--pids-limit", fmt.Sprint(pack.ProcessLimit), "--cpus", pack.CPULimit, "--memory", fmt.Sprintf("%dm", pack.MemoryLimitMB), "--security-opt", "no-new-privileges", "--cap-drop", "ALL"}
	for _, allowedPath := range pack.AllowedPaths {
		clean := filepath.Clean(allowedPath)
		hostPath := workspace.RootPath
		if clean != "." {
			hostPath = filepath.Join(workspace.RootPath, clean)
		}
		if _, err := os.Stat(hostPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		containerPath := "/workspace"
		if clean != "." {
			containerPath = filepath.Join("/workspace", clean)
		}
		args = append(args, "-v", hostPath+":"+containerPath+":rw")
	}
	args = append(args, "-w", "/workspace", pack.DockerImage)
	args = append(args, command...)
	cmd := exec.CommandContext(runCtx, "docker", args...)
	limit := pack.MaxOutputBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	writer := &boundedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = writer, writer
	err := cmd.Run()
	output := writer.String()
	if runCtx.Err() != nil {
		return output, runCtx.Err()
	}
	if err != nil {
		return output, fmt.Errorf("workspace command failed: %w", err)
	}
	return output, nil
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
			w.truncated = true
		}
		_, _ = w.buffer.Write(p)
	} else {
		w.truncated = true
	}
	return original, nil
}
func (w *boundedBuffer) String() string {
	value := w.buffer.String()
	if w.truncated {
		return value + "\n...[output truncated]"
	}
	return value
}

type WorkspaceTool struct {
	ToolName  string
	Workspace *workspacedomain.Workspace
	Pack      *workspacedomain.Pack
	Runner    WorkspaceCommandRunner
}

func (t WorkspaceTool) Name() string { return t.ToolName }
func (t WorkspaceTool) Description() string {
	return map[string]string{"list_files": "List files below an allowed workspace path.", "read_file": "Read a UTF-8 workspace file.", "search_files": "Search text in allowed workspace files.", "apply_patch": "Replace an exact text fragment in a workspace file atomically.", "write_file": "Write a workspace file.", "run_command": "Run an allowlisted argv command in the isolated Docker workspace.", "git_status": "Show git status in the isolated workspace.", "git_diff": "Show git diff in the isolated workspace.", "git_commit": "Create a git commit after explicit approval.", "run_tests": "Run an allowlisted test command in the isolated workspace."}[t.ToolName]
}
func (t WorkspaceTool) Parameters() json.RawMessage {
	switch t.ToolName {
	case "list_files":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"additionalProperties":false}`)
	case "read_file":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	case "search_files":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)
	case "apply_patch":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}`)
	case "write_file":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)
	case "git_commit":
		return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`)
	case "run_command", "run_tests":
		return json.RawMessage(`{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["command"],"additionalProperties":false}`)
	default:
		return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
}
func (t WorkspaceTool) Metadata() ToolMetadata {
	switch t.ToolName {
	case "list_files", "read_file", "search_files", "git_status", "git_diff":
		return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead, ExecutionClass: ExecutionSerial}
	default:
		return ToolMetadata{RiskLevel: RiskHigh, SideEffect: SideEffectWrite, ExecutionClass: ExecutionSerial, RequiresApproval: true}
	}
}
func (t WorkspaceTool) Execute(ctx context.Context, _ ToolRunContext, raw json.RawMessage) (*ToolResult, error) {
	if t.Workspace == nil || t.Pack == nil {
		return nil, fmt.Errorf("workspace runtime is not configured")
	}
	var input struct {
		Path      string   `json:"path"`
		Query     string   `json:"query"`
		OldString string   `json:"old_string"`
		NewString string   `json:"new_string"`
		Content   string   `json:"content"`
		Message   string   `json:"message"`
		Command   []string `json:"command"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch t.ToolName {
	case "list_files":
		path, err := resolveWorkspacePath(t.Workspace, t.Pack, input.Path, false)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		values := make([]map[string]any, 0, min(len(entries), 1000))
		for i, item := range entries {
			if i >= 1000 {
				break
			}
			info, _ := item.Info()
			values = append(values, map[string]any{"name": item.Name(), "is_dir": item.IsDir(), "size": func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}()})
		}
		return ResultFromValue(values)
	case "read_file":
		path, err := resolveWorkspacePath(t.Workspace, t.Pack, input.Path, false)
		if err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		limit := int64(t.Pack.MaxOutputBytes)
		data, err := io.ReadAll(io.LimitReader(file, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("file exceeds workspace output limit")
		}
		return &ToolResult{ContentText: string(data)}, nil
	case "search_files":
		root, err := resolveWorkspacePath(t.Workspace, t.Pack, input.Path, false)
		if err != nil {
			return nil, err
		}
		return searchWorkspace(root, input.Query, t.Pack.MaxOutputBytes)
	case "write_file":
		path, err := resolveWorkspacePath(t.Workspace, t.Pack, input.Path, true)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(input.Content), 0640); err != nil {
			return nil, err
		}
		return ResultFromValue(map[string]any{"path": input.Path, "bytes": len(input.Content)})
	case "apply_patch":
		path, err := resolveWorkspacePath(t.Workspace, t.Pack, input.Path, false)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if strings.Count(string(data), input.OldString) != 1 {
			return nil, fmt.Errorf("old_string must match exactly once")
		}
		updated := strings.Replace(string(data), input.OldString, input.NewString, 1)
		if err := os.WriteFile(path, []byte(updated), 0640); err != nil {
			return nil, err
		}
		return ResultFromValue(map[string]any{"path": input.Path, "changed": true})
	case "git_status":
		return t.run(ctx, []string{"git", "status", "--short"})
	case "git_diff":
		return t.run(ctx, []string{"git", "diff", "--"})
	case "git_commit":
		if strings.TrimSpace(input.Message) == "" {
			return nil, fmt.Errorf("commit message is required")
		}
		return t.runCommands(ctx, [][]string{{"git", "add", "--all"}, {"git", "-c", "user.name=AgentCanvas", "-c", "user.email=agentcanvas@local", "commit", "-m", input.Message}})
	case "run_command", "run_tests":
		return t.run(ctx, input.Command)
	}
	return nil, fmt.Errorf("unsupported workspace tool")
}
func (t WorkspaceTool) run(ctx context.Context, command []string) (*ToolResult, error) {
	runner := t.Runner
	if runner == nil {
		runner = DockerWorkspaceCommandRunner{}
	}
	output, err := runner.Run(ctx, t.Workspace, t.Pack, command)
	result := &ToolResult{ContentText: output, IsError: err != nil}
	return result, err
}
func (t WorkspaceTool) runCommands(ctx context.Context, commands [][]string) (*ToolResult, error) {
	outputs := make([]string, 0, len(commands))
	for _, command := range commands {
		result, err := t.run(ctx, command)
		if result != nil && strings.TrimSpace(result.ContentText) != "" {
			outputs = append(outputs, result.ContentText)
		}
		if err != nil {
			return &ToolResult{ContentText: strings.Join(outputs, "\n"), IsError: true}, err
		}
	}
	return &ToolResult{ContentText: strings.Join(outputs, "\n")}, nil
}

func NewWorkspaceTools(workspace *workspacedomain.Workspace, pack *workspacedomain.Pack, runner WorkspaceCommandRunner) []RuntimeTool {
	names := []string{"list_files", "read_file", "search_files", "apply_patch", "write_file", "run_command", "git_status", "git_diff", "git_commit", "run_tests"}
	tools := make([]RuntimeTool, 0, len(names))
	for _, name := range names {
		tools = append(tools, WorkspaceTool{ToolName: name, Workspace: workspace, Pack: pack, Runner: runner})
	}
	return tools
}

func resolveWorkspacePath(workspace *workspacedomain.Workspace, pack *workspacedomain.Pack, value string, allowMissing bool) (string, error) {
	root, err := filepath.EvalSymlinks(workspace.RootPath)
	if err != nil {
		return "", err
	}
	relative := filepath.Clean(strings.TrimSpace(value))
	if relative == "" {
		relative = "."
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path escapes root")
	}
	allowed := false
	for _, prefix := range pack.AllowedPaths {
		clean := filepath.Clean(prefix)
		rel, relErr := filepath.Rel(clean, relative)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("workspace path is outside pack allowlist")
	}
	candidate := filepath.Join(root, relative)
	check := candidate
	if allowMissing {
		check, err = nearestExistingAncestor(candidate)
		if err != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(check)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace symlink escapes root")
	}
	return candidate, nil
}
func nearestExistingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		if _, err := os.Lstat(current); err == nil {
			return current, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing workspace path ancestor")
		}
		current = parent
	}
}
func searchWorkspace(root, query string, maxBytes int) (*ToolResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	matches := make([]string, 0, 100)
	used := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= 100 {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		for index, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, query) {
				value := fmt.Sprintf("%s:%d:%s", path, index+1, line)
				used += len(value)
				if maxBytes > 0 && used > maxBytes {
					return nil
				}
				matches = append(matches, value)
				if len(matches) >= 100 {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ResultFromValue(matches)
}
