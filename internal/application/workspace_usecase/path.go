package workspace_usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
)

func normalizeSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-_")
	if len(value) > 64 {
		value = strings.Trim(value[:64], "-_")
	}
	return value
}

func (s *Service) canonicalAllowedPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", agenterrors.ErrInvalidInput
	}
	abs, err := canonicalPathWithExistingAncestor(path)
	if err != nil {
		return "", agenterrors.ErrInvalidInput
	}
	for _, rawRoot := range s.cfg.AllowedRoots {
		root, rootErr := filepath.Abs(filepath.Clean(strings.TrimSpace(rawRoot)))
		if rootErr != nil || root == "" {
			continue
		}
		if existing, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
			root = existing
		}
		if _, safeErr := workspacedomain.EnsureSafePath(root, abs); safeErr == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%w: project path is outside git_workspace.allowed_roots", agenterrors.ErrForbidden)
}

func canonicalPathWithExistingAncestor(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	existing := abs
	missing := make([]string, 0)
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func sameWorkspacePath(left, right string) bool {
	leftCanonical, leftErr := canonicalPathWithExistingAncestor(left)
	rightCanonical, rightErr := canonicalPathWithExistingAncestor(right)
	if leftErr == nil && rightErr == nil {
		return leftCanonical == rightCanonical
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func workspacePathKey(path string) string {
	if canonical, err := canonicalPathWithExistingAncestor(path); err == nil {
		return canonical
	}
	return filepath.Clean(path)
}

// validateWorkspaceBinding protects every persisted-path re-entry point. The
// database stores metadata, but it is never trusted to widen allowed_roots or
// redirect a Run to a different checkout.
func (s *Service) validateWorkspaceBinding(item *workspacedomain.Workspace) error {
	if item == nil || item.OwnerID <= 0 || item.ProjectID <= 0 || item.RunID <= 0 {
		return agenterrors.ErrInvalidInput
	}
	repositoryRoot, err := s.canonicalAllowedPath(item.RepositoryRoot)
	if err != nil {
		return err
	}
	if !sameWorkspacePath(repositoryRoot, item.RepositoryRoot) {
		return fmt.Errorf("%w: persisted repository root is not canonical", agenterrors.ErrConflict)
	}
	workspacePath, err := s.canonicalAllowedPath(item.WorkspacePath)
	if err != nil {
		return err
	}
	switch item.Kind {
	case workspacedomain.KindShared:
		if !sameWorkspacePath(repositoryRoot, workspacePath) {
			return fmt.Errorf("%w: shared workspace must use the project repository root", agenterrors.ErrConflict)
		}
	case workspacedomain.KindWorktree:
		if _, err := workspacedomain.EnsureSafePath(repositoryRoot, workspacePath); err != nil {
			return fmt.Errorf("%w: worktree path is outside its repository root", agenterrors.ErrForbidden)
		}
		if strings.TrimSpace(item.BranchName) == "" {
			return fmt.Errorf("%w: worktree branch is missing", agenterrors.ErrConflict)
		}
	default:
		return agenterrors.ErrInvalidInput
	}
	return nil
}

func (s *Service) validateWorkspaceProjectBinding(ctx context.Context, item *workspacedomain.Workspace) error {
	if err := s.validateWorkspaceBinding(item); err != nil {
		return err
	}
	projectItem, err := s.projects.FindByID(ctx, item.OwnerID, item.ProjectID)
	if err != nil {
		return err
	}
	projectRoot, err := s.validateProjectRepository(ctx, projectItem)
	if err != nil {
		return err
	}
	if !sameWorkspacePath(projectRoot, item.RepositoryRoot) {
		return fmt.Errorf("%w: workspace repository no longer matches its project", agenterrors.ErrConflict)
	}
	return nil
}

func (s *Service) registeredWorktree(ctx context.Context, item *workspacedomain.Workspace) (workspacedomain.GitWorktree, error) {
	trees, err := s.git.ListWorktrees(ctx, item.RepositoryRoot)
	if err != nil {
		return workspacedomain.GitWorktree{}, err
	}
	for _, tree := range trees {
		if sameWorkspacePath(tree.Path, item.WorkspacePath) && tree.Branch == item.BranchName {
			return tree, nil
		}
		if sameWorkspacePath(tree.Path, item.WorkspacePath) || tree.Branch == item.BranchName {
			return workspacedomain.GitWorktree{}, fmt.Errorf("%w: worktree path or branch is registered to another checkout", agenterrors.ErrConflict)
		}
	}
	return workspacedomain.GitWorktree{}, fmt.Errorf("%w: worktree is not registered by its project repository", agenterrors.ErrConflict)
}

func (s *Service) validateProjectRepository(ctx context.Context, item *projectdomain.Project) (string, error) {
	if item == nil || item.OwnerID <= 0 || item.ID <= 0 {
		return "", agenterrors.ErrInvalidInput
	}
	configuredRoot, err := s.canonicalAllowedPath(item.PrimaryPath)
	if err != nil {
		return "", err
	}
	actualRoot, err := s.git.RepoRoot(ctx, configuredRoot)
	if err != nil {
		return "", err
	}
	if !sameWorkspacePath(configuredRoot, actualRoot) {
		return "", fmt.Errorf("%w: project primary path is not the repository root", agenterrors.ErrConflict)
	}
	return actualRoot, nil
}
