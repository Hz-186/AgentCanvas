package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	workspacedomain "agentcanvas/internal/domain/workspace"
)

type GitTool struct {
	Kind string
	Git  GitOperations
}

type GitOperations interface {
	RepoRoot(context.Context, string) (string, error)
	Head(context.Context, string) (string, error)
	CurrentBranch(context.Context, string) string
	Status(context.Context, string) (workspacedomain.GitStatus, error)
	RuntimeStatus(context.Context, string) (branch, head string, dirty, unpushed bool, err error)
	Diff(context.Context, string, bool) (string, error)
	Log(context.Context, string, int) (string, error)
	Branches(context.Context, string) ([]string, error)
	ListWorktrees(context.Context, string) ([]workspacedomain.GitWorktree, error)
	Commit(context.Context, string, string, []string) (workspacedomain.GitCommitResult, error)
}

func (t GitTool) Name() string { return t.Kind }
func (t GitTool) Description() string {
	switch t.Kind {
	case "git_status":
		return "Inspect Git status for the active workspace."
	case "git_diff":
		return "Inspect the Git diff for the active workspace."
	case "git_log":
		return "Inspect recent commits for the active workspace."
	case "git_branch":
		return "List local branches for the active repository."
	case "git_worktree":
		return "List Git worktrees for the active repository."
	case "git_commit":
		return "Create an explicit Git commit on the active workspace branch."
	default:
		return "Unsupported Git workspace operation."
	}
}
func (t GitTool) Parameters() json.RawMessage {
	switch t.Kind {
	case "git_diff":
		return json.RawMessage(`{"type":"object","properties":{"staged":{"type":"boolean"}},"additionalProperties":false}`)
	case "git_log":
		return json.RawMessage(`{"type":"object","properties":{"limit":{"type":"number"}},"additionalProperties":false}`)
	case "git_commit":
		return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"required":["message"],"additionalProperties":false}`)
	default:
		return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
}
func (t GitTool) Metadata() ToolMetadata {
	metadata := ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead, TimeoutMS: 30000, MaxOutputBytes: 256 * 1024}
	if t.Kind == "git_commit" {
		metadata.RiskLevel, metadata.RequiresApproval, metadata.SideEffect = RiskMedium, true, SideEffectWrite
	}
	return metadata
}
func (t GitTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.Workspace == nil || !rc.Workspace.GitEnabled {
		return nil, errors.New("workspace Git access is disabled")
	}
	if t.Git == nil {
		return nil, errors.New("Git service is not configured")
	}
	if err := validateGitToolWorkspace(ctx, t.Git, rc.Workspace); err != nil {
		return nil, err
	}
	switch t.Kind {
	case "git_status":
		value, err := currentGitToolStatus(ctx, t.Git, rc.Workspace)
		statusError := ""
		if err != nil {
			statusError = err.Error()
		}
		if rc.EmitEvent != nil {
			_ = rc.EmitEvent(ctx, "git.status_changed", map[string]any{"workspace_id": rc.Workspace.ID, "project_id": rc.Workspace.ProjectID, "run_id": rc.RunID, "kind": rc.Workspace.Kind, "repo_root": rc.Workspace.RepositoryRoot, "path": rc.Workspace.WorkspacePath, "branch": value.Branch, "base_sha": rc.Workspace.BaseSHA, "head_sha": value.Head, "dirty": rc.Workspace.Dirty, "unpushed": rc.Workspace.Unpushed, "status": "ready", "error": statusError})
		}
		return resultOrError(value, err)
	case "git_diff":
		var in struct {
			Staged bool `json:"staged"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		value, err := t.Git.Diff(ctx, rc.Workspace.WorkspacePath, in.Staged)
		return resultOrError(map[string]any{"diff": value, "staged": in.Staged}, err)
	case "git_log":
		var in struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		value, err := t.Git.Log(ctx, rc.Workspace.WorkspacePath, in.Limit)
		return resultOrError(map[string]any{"log": value}, err)
	case "git_branch":
		value, err := t.Git.Branches(ctx, rc.Workspace.WorkspacePath)
		return resultOrError(map[string]any{"branches": value, "current": t.Git.CurrentBranch(ctx, rc.Workspace.WorkspacePath)}, err)
	case "git_worktree":
		value, err := t.Git.ListWorktrees(ctx, rc.Workspace.RepositoryRoot)
		return resultOrError(map[string]any{"worktrees": value}, err)
	case "git_commit":
		var in struct {
			Message string   `json:"message"`
			Paths   []string `json:"paths"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		for index, path := range in.Paths {
			if strings.TrimSpace(path) == "" || strings.HasPrefix(path, "/") {
				return nil, errors.New("commit paths must be relative and cannot escape workspace")
			}
			resolved, resolveErr := workspacePath(rc, path)
			if resolveErr != nil {
				return nil, resolveErr
			}
			relative, relativeErr := filepath.Rel(rc.Workspace.WorkspacePath, resolved)
			if relativeErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return nil, errors.New("commit paths must be relative and cannot escape workspace")
			}
			in.Paths[index] = filepath.ToSlash(relative)
		}
		currentBranch := strings.TrimSpace(t.Git.CurrentBranch(ctx, rc.Workspace.WorkspacePath))
		if currentBranch == "" {
			return nil, errors.New("git_commit is not allowed on a detached HEAD")
		}
		if currentBranch != rc.Workspace.BranchName {
			return nil, errors.New("git_commit is not allowed after the workspace branch changes")
		}
		beforeHead, _ := t.Git.Head(ctx, rc.Workspace.WorkspacePath)
		value, err := t.Git.Commit(ctx, rc.Workspace.WorkspacePath, in.Message, in.Paths)
		statusError := ""
		if err == nil {
			status, refreshErr := currentGitToolStatus(ctx, t.Git, rc.Workspace)
			if refreshErr != nil {
				statusError = refreshErr.Error()
				rc.Workspace.HeadSHA = value.Hash
			} else if status.Head != "" {
				value.Hash = status.Head
			}
		}
		if err == nil && rc.EmitEvent != nil {
			_ = rc.EmitEvent(ctx, "git.commit_created", map[string]any{"workspace_id": rc.Workspace.ID, "project_id": rc.Workspace.ProjectID, "run_id": rc.RunID, "kind": rc.Workspace.Kind, "repo_root": rc.Workspace.RepositoryRoot, "path": rc.Workspace.WorkspacePath, "branch": rc.Workspace.BranchName, "base_sha": rc.Workspace.BaseSHA, "head_sha": value.Hash, "dirty": rc.Workspace.Dirty, "unpushed": rc.Workspace.Unpushed, "status": "ready", "error": statusError, "hash": value.Hash, "message": value.Message, "paths": value.Paths})
		}
		result, resultErr := resultOrError(value, err)
		if result != nil {
			result.Metadata = map[string]any{"before_head": beforeHead, "after_head": value.Hash, "paths": value.Paths, "dirty": rc.Workspace.Dirty, "unpushed": rc.Workspace.Unpushed, "status_error": statusError}
		}
		return result, resultErr
	default:
		return nil, errors.New("unsupported Git workspace tool: " + t.Kind)
	}
}

func currentGitToolStatus(ctx context.Context, service GitOperations, workspace *WorkspaceContext) (workspacedomain.GitStatus, error) {
	status, err := service.Status(ctx, workspace.WorkspacePath)
	if err != nil {
		workspace.Dirty = true
		workspace.Unpushed = true
		return workspacedomain.GitStatus{Root: workspace.RepositoryRoot, Branch: workspace.BranchName, Dirty: true, Unpushed: true}, err
	}
	if workspace.Kind == "worktree" && workspace.BaseSHA != "" && status.Head != "" && status.Head != workspace.BaseSHA {
		status.Unpushed = true
	}
	workspace.HeadSHA = status.Head
	workspace.Dirty = status.Dirty
	workspace.Unpushed = status.Unpushed
	return status, nil
}

func validateGitToolWorkspace(ctx context.Context, service GitOperations, workspace *WorkspaceContext) error {
	if service == nil || workspace == nil || strings.TrimSpace(workspace.RepositoryRoot) == "" || strings.TrimSpace(workspace.WorkspacePath) == "" {
		return errors.New("workspace Git binding is incomplete")
	}
	repositoryRoot, err := service.RepoRoot(ctx, workspace.RepositoryRoot)
	if err != nil {
		return err
	}
	if !sameGitToolPath(repositoryRoot, workspace.RepositoryRoot) {
		return errors.New("workspace repository root binding changed")
	}
	checkoutRoot, err := service.RepoRoot(ctx, workspace.WorkspacePath)
	if err != nil {
		return err
	}
	if !sameGitToolPath(checkoutRoot, workspace.WorkspacePath) {
		return errors.New("workspace checkout root binding changed")
	}
	if !gitToolPathWithin(repositoryRoot, checkoutRoot) {
		return errors.New("workspace checkout is outside its repository binding")
	}
	switch workspace.Kind {
	case "shared":
		if !sameGitToolPath(repositoryRoot, checkoutRoot) {
			return errors.New("shared workspace no longer uses its repository root")
		}
	case "worktree":
		trees, err := service.ListWorktrees(ctx, repositoryRoot)
		if err != nil {
			return err
		}
		for _, tree := range trees {
			if sameGitToolPath(tree.Path, checkoutRoot) && tree.Branch == workspace.BranchName {
				return nil
			}
			if sameGitToolPath(tree.Path, checkoutRoot) || tree.Branch == workspace.BranchName {
				return errors.New("workspace worktree path or branch binding changed")
			}
		}
		return errors.New("workspace worktree is no longer registered")
	default:
		return errors.New("unsupported workspace kind")
	}
	return nil
}

func sameGitToolPath(left, right string) bool {
	return resolvedGitToolPath(left) == resolvedGitToolPath(right)
}

func gitToolPathWithin(root, path string) bool {
	relative, err := filepath.Rel(resolvedGitToolPath(root), resolvedGitToolPath(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolvedGitToolPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

func resultOrError(value any, err error) (*ToolResult, error) {
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(value)
}
