package workspace_usecase

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func (s *Service) WorkspaceContext(item *workspacedomain.Workspace) *workspacedomain.Context {
	if item == nil || item.Status != workspacedomain.StatusReady {
		return nil
	}
	return &workspacedomain.Context{ID: item.ID, ProjectID: item.ProjectID, RunID: item.RunID, Kind: item.Kind, RepositoryRoot: item.RepositoryRoot, WorkspacePath: item.WorkspacePath, BranchName: item.BranchName, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Dirty: item.Dirty, Unpushed: item.Unpushed, FileWriteEnabled: true, GitEnabled: true, ExecEnabled: true}
}

func (s *Service) GetWorkspace(ctx context.Context, ownerID, id int64) (*workspacedomain.Workspace, error) {
	return s.workspaces.FindByID(ctx, ownerID, id)
}
func (s *Service) GetRunWorkspace(ctx context.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	return s.workspaces.FindByRunID(ctx, ownerID, runID)
}
func (s *Service) GitStatus(ctx context.Context, item *workspacedomain.Workspace) (workspacedomain.GitStatus, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return workspacedomain.GitStatus{}, err
	}
	if item.Kind == workspacedomain.KindWorktree {
		if _, err := s.registeredWorktree(ctx, item); err != nil {
			return workspacedomain.GitStatus{}, err
		}
	}
	status, err := s.git.Status(ctx, item.WorkspacePath)
	if err == nil && item.Kind == workspacedomain.KindWorktree && item.BaseSHA != "" && status.Head != "" && status.Head != item.BaseSHA {
		status.Unpushed = true
	}
	return status, err
}
func (s *Service) ProjectGitStatus(ctx context.Context, item *projectdomain.Project) (workspacedomain.GitStatus, error) {
	root, err := s.validateProjectRepository(ctx, item)
	if err != nil {
		return workspacedomain.GitStatus{}, err
	}
	return s.git.Status(ctx, root)
}
func (s *Service) GitDiff(ctx context.Context, item *workspacedomain.Workspace, staged bool) (string, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return "", err
	}
	if item.Kind == workspacedomain.KindWorktree {
		if _, err := s.registeredWorktree(ctx, item); err != nil {
			return "", err
		}
	}
	return s.git.Diff(ctx, item.WorkspacePath, staged)
}
func (s *Service) GitLog(ctx context.Context, item *workspacedomain.Workspace, limit int) (string, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return "", err
	}
	if item.Kind == workspacedomain.KindWorktree {
		if _, err := s.registeredWorktree(ctx, item); err != nil {
			return "", err
		}
	}
	return s.git.Log(ctx, item.WorkspacePath, limit)
}
func (s *Service) GitBranches(ctx context.Context, item *projectdomain.Project) ([]string, error) {
	root, err := s.validateProjectRepository(ctx, item)
	if err != nil {
		return nil, err
	}
	return s.git.Branches(ctx, root)
}
func (s *Service) GitWorktrees(ctx context.Context, item *projectdomain.Project) ([]workspacedomain.GitWorktree, error) {
	root, err := s.validateProjectRepository(ctx, item)
	if err != nil {
		return nil, err
	}
	return s.git.ListWorktrees(ctx, root)
}
func (s *Service) Commit(ctx context.Context, item *workspacedomain.Workspace, message string, paths []string) (workspacedomain.GitCommitResult, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return workspacedomain.GitCommitResult{}, err
	}
	if item.Kind == workspacedomain.KindWorktree {
		if _, err := s.registeredWorktree(ctx, item); err != nil {
			return workspacedomain.GitCommitResult{}, err
		}
	}
	currentBranch := s.git.CurrentBranch(ctx, item.WorkspacePath)
	if strings.TrimSpace(item.BranchName) == "" || currentBranch != item.BranchName {
		return workspacedomain.GitCommitResult{}, fmt.Errorf("%w: workspace branch changed from %q to %q", agenterrors.ErrConflict, item.BranchName, currentBranch)
	}
	beforeHead, _ := s.git.Head(ctx, item.WorkspacePath)
	result, err := s.git.Commit(ctx, item.WorkspacePath, message, paths)
	if err == nil {
		refreshTarget := item
		if item.Kind == workspacedomain.KindShared {
			if persisted, findErr := s.workspaces.FindByID(ctx, item.OwnerID, item.ID); findErr == nil {
				refreshTarget = persisted
			}
		}
		refreshed, refreshErr := s.RefreshGitStatus(ctx, refreshTarget)
		if refreshErr != nil {
			refreshTarget.HeadSHA, refreshTarget.Dirty, refreshTarget.Unpushed = result.Hash, true, true
			refreshTarget.ErrorMessage = "post-commit Git status could not be verified: " + refreshErr.Error()
			if updateErr := s.workspaces.Update(ctx, refreshTarget); updateErr != nil {
				return result, fmt.Errorf("commit %s succeeded but workspace status persistence failed: %w", result.Hash, errors.Join(refreshErr, updateErr))
			}
		} else {
			refreshTarget = refreshed
		}
		if refreshTarget != item {
			item.HeadSHA, item.Dirty, item.Unpushed, item.LastCheckedAt, item.ErrorMessage = refreshTarget.HeadSHA, refreshTarget.Dirty, refreshTarget.Unpushed, refreshTarget.LastCheckedAt, refreshTarget.ErrorMessage
		}
		detail := workspaceAuditDetail(item)
		detail["message"], detail["paths"], detail["before_head"], detail["after_head"] = result.Message, result.Paths, beforeHead, result.Hash
		s.audit(ctx, item.OwnerID, "git.commit_created", "workspace", item.ID, detail)
	}
	return result, err
}

func workspaceAuditDetail(item *workspacedomain.Workspace) map[string]any {
	return map[string]any{"run_id": item.RunID, "project_id": item.ProjectID, "kind": item.Kind, "repo_root": item.RepositoryRoot, "path": item.WorkspacePath, "branch": item.BranchName, "base_sha": item.BaseSHA, "head_sha": item.HeadSHA, "status": item.Status, "dirty": item.Dirty, "unpushed": item.Unpushed, "locked": item.Locked, "reason": item.CleanupReason, "error": item.ErrorMessage}
}

func (s *Service) ensureWorktreeIgnore(root string) error {
	path := filepath.Join(root, ".gitignore")
	entry := s.cfg.WorktreeDirName + "/"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	_, err = io.WriteString(file, prefix+entry+"\n")
	return err
}

func (s *Service) copyWorktreeIncludes(root, worktreePath string) error {
	repositoryRoot := filepath.Clean(root)
	if resolved, resolveErr := filepath.EvalSymlinks(repositoryRoot); resolveErr == nil {
		repositoryRoot = resolved
	}
	workspaceRoot := filepath.Clean(worktreePath)
	if resolved, resolveErr := filepath.EvalSymlinks(workspaceRoot); resolveErr == nil {
		workspaceRoot = resolved
	}
	file, err := os.Open(filepath.Join(root, ".worktreeinclude"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		entry := strings.TrimSpace(scanner.Text())
		if entry == "" || strings.HasPrefix(entry, "#") || filepath.IsAbs(entry) {
			continue
		}
		traversal := false
		for _, part := range strings.Split(filepath.ToSlash(entry), "/") {
			if part == ".." {
				traversal = true
				break
			}
		}
		if traversal {
			continue
		}
		src, err := workspacedomain.EnsureSafePath(repositoryRoot, filepath.Join(repositoryRoot, entry))
		if err != nil {
			continue
		}
		dst, err := workspacedomain.EnsureSafePath(workspaceRoot, filepath.Join(workspaceRoot, entry))
		if err != nil {
			continue
		}
		resolvedSrc, err := filepath.EvalSymlinks(src)
		if err != nil {
			continue
		}
		if _, err := workspacedomain.EnsureInside(repositoryRoot, resolvedSrc); err != nil {
			continue
		}
		info, err := os.Stat(resolvedSrc)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if info.IsDir() {
			_ = copyIncludedDirectory(repositoryRoot, workspaceRoot, resolvedSrc, dst)
			continue
		}
		data, err := os.ReadFile(resolvedSrc)
		if err == nil {
			_ = os.WriteFile(dst, data, info.Mode().Perm())
		}
	}
	return scanner.Err()
}

func copyIncludedDirectory(repoRoot, workspaceRoot, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target, err := workspacedomain.EnsureSafePath(workspaceRoot, filepath.Join(destination, relative))
		if err != nil {
			return err
		}
		resolved := path
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err = filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if _, err := workspacedomain.EnsureInside(repoRoot, resolved); err != nil {
				return err
			}
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
