package workspace_usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func NormalizeMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return workspacedomain.KindShared, nil
	}
	if mode != workspacedomain.KindShared && mode != workspacedomain.KindWorktree {
		return "", agenterrors.ErrInvalidInput
	}
	return mode, nil
}

func (s *Service) PrepareRunWorkspace(ctx context.Context, ownerID, projectID, runID int64, mode, projectSlug, task string, parent *workspacedomain.Workspace) (*workspacedomain.Workspace, error) {
	if !s.cfg.Enabled || ownerID <= 0 || projectID <= 0 || runID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if existing, err := s.workspaces.FindByRunID(ctx, ownerID, runID); err == nil {
		resolved, resolveErr := s.ResolveExistingWorkspace(ctx, existing)
		if resolveErr == nil && resolved.Kind == workspacedomain.KindWorktree {
			resolveErr = s.AcquireRunWorkspaceLock(ctx, resolved, runID)
		}
		return resolved, resolveErr
	} else if !errors.Is(err, agenterrors.ErrNotFound) {
		return nil, err
	}
	projectItem, err := s.projects.FindByID(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	if projectItem.Archived {
		return nil, fmt.Errorf("%w: archived projects cannot create workspaces", agenterrors.ErrForbidden)
	}
	mode, err = NormalizeMode(mode)
	if err != nil {
		return nil, err
	}
	if parent != nil && parent.Kind == workspacedomain.KindWorktree {
		if mode == workspacedomain.KindShared {
			return nil, fmt.Errorf("%w: a worktree child cannot downgrade to shared", agenterrors.ErrForbidden)
		}
		mode = workspacedomain.KindWorktree
	}
	workspace := &workspacedomain.Workspace{
		BaseModel: domain.BaseModel{OwnerID: ownerID}, ProjectID: projectID, RunID: runID, Kind: mode,
		RepositoryRoot: projectItem.RepositoryRoot, WorkspacePath: projectItem.RepositoryRoot,
		Status: workspacedomain.StatusCreating,
	}
	if parent != nil {
		workspace.ParentWorkspaceID = &parent.ID
	}
	primaryPath, err := s.canonicalAllowedPath(projectItem.RepositoryRoot)
	if err != nil {
		return s.failWorkspace(ctx, workspace, err)
	}
	workspace.RepositoryRoot, workspace.WorkspacePath = primaryPath, primaryPath
	root, err := s.git.EnsureRepository(ctx, primaryPath, s.cfg.AutoInitRepository)
	if err != nil {
		if mode == workspacedomain.KindWorktree {
			workspace.BranchName = workspacedomain.BranchName(projectSlug, runID, task)
			workspace.WorkspacePath = filepath.Join(projectItem.RepositoryRoot, s.cfg.WorktreeDirName, workspacedomain.Slugify(fmt.Sprintf("%d-%s", runID, task)))
		}
		return s.failWorkspace(ctx, workspace, err)
	}
	if _, err := s.canonicalAllowedPath(root); err != nil {
		workspace.RepositoryRoot = root
		return s.failWorkspace(ctx, workspace, err)
	}
	workspace.RepositoryRoot = root
	if mode == workspacedomain.KindShared {
		workspace.WorkspacePath = root
		if err := s.workspaces.Create(ctx, workspace); err != nil {
			return nil, err
		}
		s.audit(ctx, ownerID, "workspace.created", "workspace", workspace.ID, workspaceAuditDetail(workspace))
		status, statusErr := s.git.Status(ctx, root)
		if statusErr != nil {
			return s.failWorkspace(ctx, workspace, statusErr)
		}
		workspace.WorkspacePath, workspace.BranchName, workspace.BaseSHA, workspace.HeadSHA = root, status.Branch, status.Head, status.Head
		workspace.Dirty, workspace.HasUnpushedCommits, workspace.Status = status.Dirty, status.HasUnpushedCommits, workspacedomain.StatusReady
		if err := s.workspaces.Update(ctx, workspace); err != nil {
			return nil, err
		}
		s.audit(ctx, ownerID, "workspace.ready", "workspace", workspace.ID, workspaceAuditDetail(workspace))
		return workspace, nil
	}
	active, err := s.workspaces.ListByProject(ctx, ownerID, projectID)
	if err != nil {
		workspace.BranchName = workspacedomain.BranchName(projectSlug, runID, task)
		workspace.WorkspacePath = filepath.Join(root, s.cfg.WorktreeDirName, workspacedomain.Slugify(fmt.Sprintf("%d-%s", runID, task)))
		return s.failWorkspace(ctx, workspace, err)
	}
	activeCount := 0
	for _, candidate := range active {
		if candidate.Kind == workspacedomain.KindWorktree && (candidate.Status == workspacedomain.StatusCreating || candidate.Status == workspacedomain.StatusReady || candidate.Status == workspacedomain.StatusPreserved) {
			activeCount++
		}
	}
	branchBase := workspacedomain.BranchName(projectSlug, runID, task)
	dirBase, err := workspacedomain.EnsureSafePath(root, filepath.Join(root, s.cfg.WorktreeDirName, workspacedomain.Slugify(fmt.Sprintf("%d-%s", runID, task))))
	if err != nil {
		workspace.BranchName, workspace.WorkspacePath = branchBase, filepath.Join(root, s.cfg.WorktreeDirName, workspacedomain.Slugify(fmt.Sprintf("%d-%s", runID, task)))
		return s.failWorkspace(ctx, workspace, err)
	}
	branch, path, err := s.uniqueWorktreeTarget(ctx, root, branchBase, dirBase, active)
	if err != nil {
		workspace.BranchName, workspace.WorkspacePath = branchBase, dirBase
		return s.failWorkspace(ctx, workspace, err)
	}
	workspace.BranchName, workspace.WorkspacePath = branch, path
	if activeCount >= s.cfg.MaxWorkspacesPerProject {
		return s.failWorkspace(ctx, workspace, fmt.Errorf("%w: project worktree limit reached", agenterrors.ErrForbidden))
	}
	baseRef, _ := s.git.ResolveBase(ctx, root)
	baseSHA, err := s.git.ResolveCommit(ctx, root, baseRef)
	if err != nil {
		baseRef = "HEAD"
		baseSHA, err = s.git.Head(ctx, root)
		if err != nil {
			return s.failWorkspace(ctx, workspace, err)
		}
	}
	workspace.BaseRef, workspace.BaseSHA = baseRef, baseSHA
	reserved := false
	for attempt := 0; attempt < 32; attempt++ {
		candidateBranchBase, candidateDirBase := branchBase, dirBase
		if attempt > 0 {
			candidateBranchBase = fmt.Sprintf("%s-%d", branchBase, attempt+1)
			candidateDirBase = fmt.Sprintf("%s-%d", dirBase, attempt+1)
		}
		branch, path, err = s.uniqueWorktreeTarget(ctx, root, candidateBranchBase, candidateDirBase, active)
		if err != nil {
			workspace.BranchName, workspace.WorkspacePath = candidateBranchBase, candidateDirBase
			return s.failWorkspace(ctx, workspace, err)
		}
		workspace.WorkspacePath, workspace.BranchName = path, branch
		if err = s.workspaces.Create(ctx, workspace); err == nil {
			reserved = true
			break
		}
		workspace.ID = 0
		if !errors.Is(err, agenterrors.ErrConflict) {
			return nil, err
		}
		if existing, findErr := s.workspaces.FindByRunID(ctx, ownerID, runID); findErr == nil {
			resolved, resolveErr := s.ResolveExistingWorkspace(ctx, existing)
			if resolveErr == nil && resolved.Kind == workspacedomain.KindWorktree {
				resolveErr = s.AcquireRunWorkspaceLock(ctx, resolved, runID)
			}
			return resolved, resolveErr
		}
		if refreshed, listErr := s.workspaces.ListByProject(ctx, ownerID, projectID); listErr == nil {
			active = refreshed
		}
	}
	if !reserved {
		return nil, fmt.Errorf("%w: could not reserve a unique worktree branch and path", agenterrors.ErrConflict)
	}
	branch, path = workspace.BranchName, workspace.WorkspacePath
	s.audit(ctx, ownerID, "workspace.created", "workspace", workspace.ID, workspaceAuditDetail(workspace))
	if err := s.ensureWorktreeIgnore(root); err != nil { /* best effort, like Hermes */
	}
	if err := s.git.AddWorktree(ctx, root, path, branch, baseRef); err != nil {
		return s.failWorkspace(ctx, workspace, err)
	}
	_ = s.copyWorktreeIncludes(root, path)
	workspace.LockReason = runWorkspaceLockReason(runID)
	if err := s.git.LockWorktree(ctx, root, path, workspace.LockReason); err == nil {
		workspace.Locked = true
	} else {
		// An unverifiable lock is fail-safe: the run may proceed, but cleanup
		// must preserve the checkout until an operator can inspect it.
		workspace.Locked = true
		workspace.LockReason = "lock could not be established: " + err.Error()
	}
	workspace.HeadSHA, err = s.git.Head(ctx, path)
	if err != nil {
		return s.failWorkspace(ctx, workspace, err)
	}
	workspace.Status = workspacedomain.StatusReady
	if err := s.workspaces.Update(ctx, workspace); err != nil {
		unlockErr := s.git.UnlockWorktree(context.WithoutCancel(ctx), root, path)
		workspace.Locked, workspace.LockReason = false, ""
		return nil, errors.Join(err, unlockErr)
	}
	s.audit(ctx, ownerID, "workspace.ready", "workspace", workspace.ID, workspaceAuditDetail(workspace))
	return workspace, nil
}

func (s *Service) PrepareChildWorkspace(ctx context.Context, ownerID, projectID, runID int64, requestedMode, projectSlug, task string, parent *workspacedomain.Workspace) (*workspacedomain.Workspace, error) {
	if parent != nil {
		resolved, err := s.ResolveExistingWorkspace(ctx, parent)
		if err != nil {
			return nil, err
		}
		parent = resolved
	}
	requested := strings.TrimSpace(requestedMode)
	if parent != nil && parent.Kind == workspacedomain.KindShared && (requested == "" || requested == "inherit" || requested == workspacedomain.KindShared) {
		// Shared delegation inherits the physical checkout. Return an immutable
		// per-run view while retaining one database row for the shared workspace.
		view := *parent
		view.RunID = runID
		return &view, nil
	}
	if requestedMode == "" || requestedMode == "inherit" {
		requestedMode = workspacedomain.KindShared
		if parent != nil {
			requestedMode = parent.Kind
		}
	}
	return s.PrepareRunWorkspace(ctx, ownerID, projectID, runID, requestedMode, projectSlug, task, parent)
}

func (s *Service) uniqueWorktreeTarget(ctx context.Context, root, branchBase, dirBase string, persisted []workspacedomain.Workspace) (string, string, error) {
	trees, err := s.git.ListWorktrees(ctx, root)
	if err != nil {
		return "", "", err
	}
	branches, err := s.git.Branches(ctx, root)
	if err != nil {
		return "", "", err
	}
	usedBranch, usedPath := map[string]bool{}, map[string]bool{}
	for _, tree := range trees {
		usedBranch[tree.Branch], usedPath[workspacePathKey(tree.Path)] = true, true
	}
	for _, branch := range branches {
		usedBranch[branch] = true
	}
	for _, item := range persisted {
		if item.Kind == workspacedomain.KindWorktree && sameWorkspacePath(item.RepositoryRoot, root) {
			usedBranch[item.BranchName], usedPath[workspacePathKey(item.WorkspacePath)] = true, true
		}
	}
	for suffix := 0; suffix < 10000; suffix++ {
		branch, path := branchBase, dirBase
		if suffix > 0 {
			branch = fmt.Sprintf("%s-%d", branchBase, suffix+1)
			path = fmt.Sprintf("%s-%d", dirBase, suffix+1)
		}
		_, statErr := os.Stat(path)
		if !usedBranch[branch] && !usedPath[workspacePathKey(path)] && errors.Is(statErr, os.ErrNotExist) {
			return branch, path, nil
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", "", statErr
		}
	}
	return "", "", fmt.Errorf("could not allocate a unique worktree branch and path")
}

func (s *Service) failWorkspace(ctx context.Context, item *workspacedomain.Workspace, cause error) (*workspacedomain.Workspace, error) {
	item.Status, item.ErrorMessage = workspacedomain.StatusFailed, cause.Error()
	var persistErr error
	if item.ID == 0 {
		persistErr = s.workspaces.Create(ctx, item)
	} else {
		persistErr = s.workspaces.Update(ctx, item)
	}
	if persistErr != nil {
		return nil, fmt.Errorf("%w (persist workspace failure: %v)", cause, persistErr)
	}
	s.audit(ctx, item.OwnerID, "workspace.failed", "workspace", item.ID, workspaceAuditDetail(item))
	return item, cause
}

func (s *Service) ResolveExistingWorkspace(ctx context.Context, item *workspacedomain.Workspace) (*workspacedomain.Workspace, error) {
	if item == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return nil, err
	}
	if item.Kind == workspacedomain.KindShared {
		if _, err := s.git.RepoRoot(ctx, item.WorkspacePath); err != nil {
			return nil, err
		}
		return s.RefreshGitStatus(ctx, item)
	}
	trees, err := s.git.ListWorktrees(ctx, item.RepositoryRoot)
	if err != nil {
		return nil, err
	}
	for _, tree := range trees {
		if sameWorkspacePath(tree.Path, item.WorkspacePath) && tree.Branch == item.BranchName {
			if item.Status == workspacedomain.StatusFailed {
				item.Status = workspacedomain.StatusCreating
			}
			return s.RefreshGitStatus(ctx, item)
		}
		if sameWorkspacePath(tree.Path, item.WorkspacePath) || tree.Branch == item.BranchName {
			return s.failWorkspace(ctx, item, fmt.Errorf("persisted worktree path or branch is occupied by another checkout"))
		}
	}
	if item.Status == workspacedomain.StatusCreating {
		if _, statErr := os.Lstat(item.WorkspacePath); statErr == nil {
			return s.failWorkspace(ctx, item, fmt.Errorf("persisted worktree path exists but is not registered by Git"))
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return s.failWorkspace(ctx, item, statErr)
		}
		_ = s.ensureWorktreeIgnore(item.RepositoryRoot)
		if err := s.git.AddWorktree(ctx, item.RepositoryRoot, item.WorkspacePath, item.BranchName, item.BaseRef); err != nil {
			// API and worker processes can recover the same creating row at the
			// same time. If another process completed the exact persisted
			// checkout, reuse it; an occupied path or branch with any other
			// binding remains a hard failure.
			trees, listErr := s.git.ListWorktrees(ctx, item.RepositoryRoot)
			if listErr != nil {
				return s.failWorkspace(ctx, item, errors.Join(err, listErr))
			}
			recovered := false
			for _, tree := range trees {
				if sameWorkspacePath(tree.Path, item.WorkspacePath) && tree.Branch == item.BranchName {
					recovered = true
					break
				}
				if sameWorkspacePath(tree.Path, item.WorkspacePath) || tree.Branch == item.BranchName {
					return s.failWorkspace(ctx, item, fmt.Errorf("persisted worktree path or branch is occupied by another checkout"))
				}
			}
			if !recovered {
				return s.failWorkspace(ctx, item, err)
			}
		}
		_ = s.copyWorktreeIncludes(item.RepositoryRoot, item.WorkspacePath)
		item.HeadSHA, err = s.git.Head(ctx, item.WorkspacePath)
		if err != nil {
			return s.failWorkspace(ctx, item, err)
		}
		item.Status, item.ErrorMessage, item.Locked, item.LockReason = workspacedomain.StatusReady, "", false, ""
		if err := s.workspaces.Update(ctx, item); err != nil {
			return nil, err
		}
		s.audit(ctx, item.OwnerID, "workspace.ready", "workspace", item.ID, workspaceAuditDetail(item))
		return item, nil
	}
	_, err = s.failWorkspace(ctx, item, fmt.Errorf("persisted worktree is missing or points to another branch"))
	return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
}

func (s *Service) RefreshGitStatus(ctx context.Context, item *workspacedomain.Workspace) (*workspacedomain.Workspace, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return nil, err
	}
	status, err := s.git.Status(ctx, item.WorkspacePath)
	if err != nil {
		now := time.Now().UTC()
		item.Dirty, item.HasUnpushedCommits, item.LastCheckedAt, item.ErrorMessage = true, true, &now, err.Error()
		persistErr := s.workspaces.Update(ctx, item)
		s.audit(ctx, item.OwnerID, "workspace.status_failed", "workspace", item.ID, workspaceAuditDetail(item))
		return nil, errors.Join(err, persistErr)
	}
	now := time.Now().UTC()
	if item.Status == workspacedomain.StatusCreating && item.BranchName == "" {
		item.BranchName = status.Branch
	} else if status.Branch != item.BranchName {
		err = fmt.Errorf("%w: workspace branch changed from %q to %q", agenterrors.ErrConflict, item.BranchName, status.Branch)
		item.HeadSHA, item.Dirty, item.HasUnpushedCommits, item.LastCheckedAt, item.ErrorMessage = status.Head, true, true, &now, err.Error()
		persistErr := s.workspaces.Update(ctx, item)
		s.audit(ctx, item.OwnerID, "workspace.status_failed", "workspace", item.ID, workspaceAuditDetail(item))
		return nil, errors.Join(err, persistErr)
	}
	item.HeadSHA, item.Dirty, item.HasUnpushedCommits, item.LastCheckedAt, item.ErrorMessage = status.Head, status.Dirty, status.HasUnpushedCommits, &now, ""
	if item.Kind == workspacedomain.KindWorktree && item.BaseSHA != "" && item.HeadSHA != "" && item.HeadSHA != item.BaseSHA {
		// A worktree branch without an upstream cannot prove that its commits
		// are published. Treat any commit beyond the recorded base as unpushed.
		item.HasUnpushedCommits = true
	}
	if item.Kind == workspacedomain.KindWorktree {
		tree, listErr := s.registeredWorktree(ctx, item)
		if listErr != nil {
			item.Dirty, item.HasUnpushedCommits, item.ErrorMessage = true, true, listErr.Error()
			persistErr := s.workspaces.Update(ctx, item)
			s.audit(ctx, item.OwnerID, "workspace.status_failed", "workspace", item.ID, workspaceAuditDetail(item))
			return nil, errors.Join(listErr, persistErr)
		}
		item.Locked, item.LockReason = tree.Locked, tree.LockReason
	}
	if item.Status == workspacedomain.StatusCreating {
		item.Status = workspacedomain.StatusReady
	}
	if err := s.workspaces.Update(ctx, item); err != nil {
		return nil, err
	}
	s.audit(ctx, item.OwnerID, "workspace.status_changed", "workspace", item.ID, workspaceAuditDetail(item))
	return item, nil
}
