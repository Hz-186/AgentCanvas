package workspace_usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func (s *Service) CleanupRunWorkspace(ctx context.Context, ownerID, runID int64, force bool) (*workspacedomain.Workspace, error) {
	item, err := s.workspaces.FindByRunID(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	if item.Kind == workspacedomain.KindShared {
		return s.preserveWorkspace(ctx, item, "shared workspace is never removed", nil)
	}
	if _, err := s.RefreshGitStatus(ctx, item); err != nil {
		item.ErrorMessage = err.Error()
		return s.preserveWorkspace(ctx, item, "git status could not be verified", nil)
	}
	if item.Locked {
		live, known := lockProcessState(item.LockReason)
		if !known || live {
			return s.preserveWorkspace(ctx, item, "workspace lock is live or cannot be verified", nil)
		}
		if err := s.git.UnlockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath); err != nil {
			return s.preserveWorkspace(ctx, item, "dead workspace lock could not be removed", err)
		}
		item.Locked, item.LockReason = false, ""
	}
	// Neither force nor configuration may bypass the fail-safe preservation
	// policy. The flags remain in configuration as explicit operator intent,
	// but dirty or unpublished work is never deleted.
	if item.Dirty || item.HasUnpushedCommits {
		return s.preserveWorkspace(ctx, item, "workspace contains dirty or unpushed work", nil)
	}
	if err := s.git.RemoveWorktree(ctx, item.RepositoryRoot, item.WorkspacePath, false); err != nil {
		return s.preserveWorkspace(ctx, item, "git worktree remove failed", err)
	}
	now := time.Now().UTC()
	item.Status, item.CleanupReason, item.CleanedAt, item.Locked, item.LockReason, item.ErrorMessage = workspacedomain.StatusCleaned, "checkout removed; branch retained", &now, false, "", ""
	if err := s.workspaces.Update(ctx, item); err != nil {
		return item, err
	}
	s.audit(ctx, item.OwnerID, "workspace.cleaned", "workspace", item.ID, workspaceAuditDetail(item))
	return item, nil
}

func (s *Service) preserveWorkspace(ctx context.Context, item *workspacedomain.Workspace, reason string, cause error) (*workspacedomain.Workspace, error) {
	item.Status, item.CleanupReason = workspacedomain.StatusPreserved, reason
	if cause != nil {
		item.ErrorMessage = cause.Error()
	}
	persistErr := s.workspaces.Update(ctx, item)
	s.audit(ctx, item.OwnerID, "workspace.preserved", "workspace", item.ID, workspaceAuditDetail(item))
	return item, errors.Join(cause, persistErr)
}

// ReleaseRunWorkspaceLock is called when a run has stopped using its checkout.
// The lock is held for the lifetime of the run and is never used to bypass the
// fail-safe cleanup checks for a still-live or unverifiable process.
func (s *Service) ReleaseRunWorkspaceLock(ctx context.Context, ownerID, runID int64) error {
	item, err := s.workspaces.FindByRunID(ctx, ownerID, runID)
	if err != nil || item.Kind != workspacedomain.KindWorktree || !item.Locked {
		return err
	}
	if item.LockReason != runWorkspaceLockReason(runID) {
		// A lock that cannot be proven to belong to this process must remain in
		// place for operator review. Never unlock a foreign or unknown owner.
		return nil
	}
	if err := s.git.UnlockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath); err != nil {
		return err
	}
	item.Locked, item.LockReason = false, ""
	if err := s.workspaces.Update(ctx, item); err != nil {
		return err
	}
	s.audit(ctx, item.OwnerID, "workspace.status_changed", "workspace", item.ID, workspaceAuditDetail(item))
	return nil
}

// AcquireRunWorkspaceLock re-establishes the process lock before a persisted
// run resumes. A live or unverifiable lock is never stolen.
func (s *Service) AcquireRunWorkspaceLock(ctx context.Context, item *workspacedomain.Workspace, runID int64) error {
	if item == nil {
		return agenterrors.ErrInvalidInput
	}
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return err
	}
	if item.Kind != workspacedomain.KindWorktree {
		return nil
	}
	reason := runWorkspaceLockReason(runID)
	if item.Locked {
		live, known := lockProcessState(item.LockReason)
		if !known {
			return fmt.Errorf("%w: workspace lock cannot be verified", agenterrors.ErrForbidden)
		}
		if live {
			if item.LockReason == reason {
				return nil
			}
			return fmt.Errorf("%w: workspace is locked by a live process", agenterrors.ErrForbidden)
		}
		if err := s.git.UnlockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath); err != nil {
			return err
		}
	}
	if err := s.git.LockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath, reason); err != nil {
		return err
	}
	item.Locked, item.LockReason = true, reason
	if err := s.workspaces.Update(ctx, item); err != nil {
		unlockErr := s.git.UnlockWorktree(context.WithoutCancel(ctx), item.RepositoryRoot, item.WorkspacePath)
		item.Locked, item.LockReason = false, ""
		return errors.Join(err, unlockErr)
	}
	s.audit(ctx, item.OwnerID, "workspace.status_changed", "workspace", item.ID, workspaceAuditDetail(item))
	return nil
}

func lockProcessState(reason string) (live bool, known bool) {
	reason = strings.TrimSpace(reason)
	host := lockReasonValue(reason, "host=")
	currentHost, hostErr := os.Hostname()
	if host == "" || hostErr != nil || currentHost == "" || host != currentHost {
		return false, false
	}
	value := lockReasonValue(reason, "pid=")
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return false, false
	}
	err = syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true, true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, true
	}
	return true, true
}

func runWorkspaceLockReason(runID int64) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return fmt.Sprintf("agentcanvas run=%d pid=%d", runID, os.Getpid())
	}
	return fmt.Sprintf("agentcanvas run=%d pid=%d host=%s", runID, os.Getpid(), host)
}

func lockReasonValue(reason, marker string) string {
	idx := strings.Index(reason, marker)
	if idx < 0 {
		return ""
	}
	value := reason[idx+len(marker):]
	if end := strings.IndexAny(value, " \t,"); end >= 0 {
		value = value[:end]
	}
	return value
}

func (s *Service) RecoverAfterRestart(ctx context.Context, ownerID, projectID int64) error {
	var (
		items []workspacedomain.Workspace
		err   error
	)
	if ownerID > 0 && projectID > 0 {
		items, err = s.workspaces.ListByProject(ctx, ownerID, projectID)
	} else {
		// Startup recovery must inspect every persisted active workspace. A
		// partial batch would leave older checkouts permanently unrecovered.
		items, err = s.workspaces.ListRecoverable(ctx, 0)
	}
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for i := range items {
		if items[i].Status == workspacedomain.StatusReady || items[i].Status == workspacedomain.StatusCreating {
			if _, resolveErr := s.ResolveExistingWorkspace(ctx, &items[i]); resolveErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("recover workspace %d (run %d): %w", items[i].ID, items[i].RunID, resolveErr))
			}
		}
	}
	return errors.Join(recoveryErrors...)
}

func (s *Service) PruneStaleWorkspaces(ctx context.Context) error {
	items, err := s.workspaces.ListStale(ctx, time.Now().UTC().Add(-s.cfg.PruneTTL), 100)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for i := range items {
		if _, cleanupErr := s.CleanupRunWorkspace(ctx, items[i].OwnerID, items[i].RunID, false); cleanupErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup workspace %d (run %d): %w", items[i].ID, items[i].RunID, cleanupErr))
		}
	}
	return errors.Join(cleanupErrors...)
}
