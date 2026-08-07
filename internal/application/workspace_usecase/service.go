package workspace_usecase

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentcanvas/internal/domain/audit"
	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
	gitinfra "agentcanvas/internal/infrastructure/git"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type Config struct {
	Enabled                 bool
	AllowedRoots            []string
	WorktreeDirName         string
	MaxWorkspacesPerProject int
	PruneTTL                time.Duration
	PreserveDirty           bool
	PreserveUnpushed        bool
	AutoInitRepository      bool
}

type Service struct {
	projects   projectdomain.Repository
	workspaces workspacedomain.Repository
	git        *gitinfra.Service
	audits     audit.Repository
	cfg        Config
}

func (s *Service) ConfigureAudits(repository audit.Repository) { s.audits = repository }

func (s *Service) audit(ctx context.Context, ownerID int64, action, resourceType string, resourceID int64, detail map[string]any) {
	if s.audits == nil || ownerID <= 0 {
		return
	}
	encoded, _ := json.Marshal(detail)
	_ = s.audits.Create(ctx, &audit.Log{OwnerID: ownerID, ActorID: ownerID, Action: action, ResourceType: resourceType, ResourceID: strconv.FormatInt(resourceID, 10), DetailJSON: string(encoded)})
}

type CreateProjectRequest struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Icon          string `json:"icon"`
	Color         string `json:"color"`
	PrimaryPath   string `json:"primary_path"`
	InitializeGit *bool  `json:"initialize_git,omitempty"`
}

type UpdateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Icon        *string `json:"icon"`
	Color       *string `json:"color"`
}

type AddFolderRequest struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	IsPrimary bool   `json:"is_primary"`
}

func NewService(projects projectdomain.Repository, workspaces workspacedomain.Repository, gitService *gitinfra.Service, cfg Config) *Service {
	if cfg.WorktreeDirName == "" {
		cfg.WorktreeDirName = ".worktrees"
	}
	if cfg.MaxWorkspacesPerProject <= 0 {
		cfg.MaxWorkspacesPerProject = 64
	}
	if cfg.PruneTTL <= 0 {
		cfg.PruneTTL = 24 * time.Hour
	}
	return &Service{projects: projects, workspaces: workspaces, git: gitService, cfg: cfg}
}

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
		if _, safeErr := gitinfra.EnsureSafePath(root, abs); safeErr == nil {
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
		if _, err := gitinfra.EnsureSafePath(repositoryRoot, workspacePath); err != nil {
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

func (s *Service) registeredWorktree(ctx context.Context, item *workspacedomain.Workspace) (gitinfra.Worktree, error) {
	trees, err := s.git.ListWorktrees(ctx, item.RepositoryRoot)
	if err != nil {
		return gitinfra.Worktree{}, err
	}
	for _, tree := range trees {
		if sameWorkspacePath(tree.Path, item.WorkspacePath) && tree.Branch == item.BranchName {
			return tree, nil
		}
		if sameWorkspacePath(tree.Path, item.WorkspacePath) || tree.Branch == item.BranchName {
			return gitinfra.Worktree{}, fmt.Errorf("%w: worktree path or branch is registered to another checkout", agenterrors.ErrConflict)
		}
	}
	return gitinfra.Worktree{}, fmt.Errorf("%w: worktree is not registered by its project repository", agenterrors.ErrConflict)
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

func (s *Service) CreateProject(ctx context.Context, ownerID int64, req CreateProjectRequest) (*projectdomain.Project, error) {
	if !s.cfg.Enabled || ownerID <= 0 || strings.TrimSpace(req.Name) == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	primary, err := s.canonicalAllowedPath(req.PrimaryPath)
	if err != nil {
		return nil, err
	}
	allowInit := s.cfg.AutoInitRepository
	if req.InitializeGit != nil {
		allowInit = *req.InitializeGit
	}
	root, err := s.git.EnsureRepository(ctx, primary, allowInit)
	if err != nil {
		return nil, err
	}
	if _, err := s.canonicalAllowedPath(root); err != nil {
		return nil, err
	}
	slug := normalizeSlug(req.Slug)
	if slug == "" {
		slug = normalizeSlug(req.Name)
	}
	if slug == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &projectdomain.Project{OwnerID: ownerID, Slug: slug, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Icon: strings.TrimSpace(req.Icon), Color: strings.TrimSpace(req.Color), PrimaryPath: root}
	folder := &projectdomain.ProjectFolder{OwnerID: ownerID, ProjectID: item.ID, Path: root, Label: "Primary", IsPrimary: true}
	if err := s.projects.CreateWithPrimaryFolder(ctx, item, folder); err != nil {
		return nil, err
	}
	item.Folders = []projectdomain.ProjectFolder{*folder}
	s.audit(ctx, ownerID, "project.create", "project", item.ID, map[string]any{"slug": item.Slug, "primary_path": item.PrimaryPath})
	return item, nil
}

func (s *Service) ListProjects(ctx context.Context, ownerID int64, includeArchived bool) ([]projectdomain.Project, error) {
	return s.projects.ListByOwner(ctx, ownerID, includeArchived)
}
func (s *Service) GetProject(ctx context.Context, ownerID, id int64) (*projectdomain.Project, error) {
	return s.projects.FindByID(ctx, ownerID, id)
}

func (s *Service) UpdateProject(ctx context.Context, ownerID, id int64, req UpdateProjectRequest) (*projectdomain.Project, error) {
	item, err := s.projects.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		value := strings.TrimSpace(*req.Name)
		if value == "" {
			return nil, agenterrors.ErrInvalidInput
		}
		item.Name = value
	}
	if req.Description != nil {
		item.Description = strings.TrimSpace(*req.Description)
	}
	if req.Icon != nil {
		item.Icon = strings.TrimSpace(*req.Icon)
	}
	if req.Color != nil {
		item.Color = strings.TrimSpace(*req.Color)
	}
	if err := s.projects.Update(ctx, item); err != nil {
		return nil, err
	}
	s.audit(ctx, ownerID, "project.update", "project", item.ID, map[string]any{"name": item.Name, "archived": item.Archived})
	return item, nil
}

func (s *Service) ArchiveProject(ctx context.Context, ownerID, id int64) error {
	_, err := s.projects.FindByID(ctx, ownerID, id)
	if err != nil {
		return err
	}
	err = s.projects.Archive(ctx, ownerID, id)
	if err == nil {
		s.audit(ctx, ownerID, "project.archive", "project", id, nil)
	}
	return err
}

func (s *Service) AddFolder(ctx context.Context, ownerID, projectID int64, req AddFolderRequest) (*projectdomain.ProjectFolder, error) {
	projectItem, err := s.projects.FindByID(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	if projectItem.Archived {
		return nil, fmt.Errorf("%w: archived projects cannot be changed", agenterrors.ErrForbidden)
	}
	path, err := s.canonicalAllowedPath(req.Path)
	if err != nil {
		return nil, err
	}
	if req.IsPrimary {
		path, err = s.git.EnsureRepository(ctx, path, s.cfg.AutoInitRepository)
		if err != nil {
			return nil, err
		}
		if path, err = s.canonicalAllowedPath(path); err != nil {
			return nil, err
		}
	}
	item := &projectdomain.ProjectFolder{OwnerID: ownerID, ProjectID: projectID, Path: path, Label: strings.TrimSpace(req.Label), IsPrimary: req.IsPrimary}
	if req.IsPrimary {
		if err := s.projects.AddPrimaryFolder(ctx, item); err != nil {
			return nil, err
		}
	} else if err := s.projects.AddFolder(ctx, item); err != nil {
		return nil, err
	}
	s.audit(ctx, ownerID, "project.folder_added", "project", projectID, map[string]any{"folder_id": item.ID, "path": item.Path, "is_primary": item.IsPrimary})
	return item, nil
}

func (s *Service) DeleteFolder(ctx context.Context, ownerID, projectID, folderID int64) error {
	folders, err := s.projects.ListFolders(ctx, ownerID, projectID)
	if err != nil {
		return err
	}
	for _, folder := range folders {
		if folder.ID == folderID && folder.IsPrimary {
			return fmt.Errorf("%w: primary folder cannot be deleted", agenterrors.ErrForbidden)
		}
	}
	if err := s.projects.DeleteFolder(ctx, ownerID, projectID, folderID); err != nil {
		return err
	}
	s.audit(ctx, ownerID, "project.folder_removed", "project", projectID, map[string]any{"folder_id": folderID})
	return nil
}

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
		OwnerID: ownerID, ProjectID: projectID, RunID: runID, Kind: mode,
		RepositoryRoot: projectItem.PrimaryPath, WorkspacePath: projectItem.PrimaryPath,
		Status: workspacedomain.StatusCreating,
	}
	if parent != nil {
		workspace.ParentWorkspaceID = &parent.ID
	}
	primaryPath, err := s.canonicalAllowedPath(projectItem.PrimaryPath)
	if err != nil {
		return s.failWorkspace(ctx, workspace, err)
	}
	workspace.RepositoryRoot, workspace.WorkspacePath = primaryPath, primaryPath
	root, err := s.git.EnsureRepository(ctx, primaryPath, s.cfg.AutoInitRepository)
	if err != nil {
		if mode == workspacedomain.KindWorktree {
			workspace.BranchName = gitinfra.BranchName(projectSlug, runID, task)
			workspace.WorkspacePath = filepath.Join(projectItem.PrimaryPath, s.cfg.WorktreeDirName, gitinfra.Slugify(fmt.Sprintf("%d-%s", runID, task)))
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
		workspace.Dirty, workspace.Unpushed, workspace.Status = status.Dirty, status.Unpushed, workspacedomain.StatusReady
		if err := s.workspaces.Update(ctx, workspace); err != nil {
			return nil, err
		}
		s.audit(ctx, ownerID, "workspace.ready", "workspace", workspace.ID, workspaceAuditDetail(workspace))
		return workspace, nil
	}
	active, err := s.workspaces.ListByProject(ctx, ownerID, projectID)
	if err != nil {
		workspace.BranchName = gitinfra.BranchName(projectSlug, runID, task)
		workspace.WorkspacePath = filepath.Join(root, s.cfg.WorktreeDirName, gitinfra.Slugify(fmt.Sprintf("%d-%s", runID, task)))
		return s.failWorkspace(ctx, workspace, err)
	}
	activeCount := 0
	for _, candidate := range active {
		if candidate.Kind == workspacedomain.KindWorktree && (candidate.Status == workspacedomain.StatusCreating || candidate.Status == workspacedomain.StatusReady || candidate.Status == workspacedomain.StatusPreserved) {
			activeCount++
		}
	}
	branchBase := gitinfra.BranchName(projectSlug, runID, task)
	dirBase, err := gitinfra.EnsureSafePath(root, filepath.Join(root, s.cfg.WorktreeDirName, gitinfra.Slugify(fmt.Sprintf("%d-%s", runID, task))))
	if err != nil {
		workspace.BranchName, workspace.WorkspacePath = branchBase, filepath.Join(root, s.cfg.WorktreeDirName, gitinfra.Slugify(fmt.Sprintf("%d-%s", runID, task)))
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
		item.Dirty, item.Unpushed, item.LastCheckedAt, item.ErrorMessage = true, true, &now, err.Error()
		persistErr := s.workspaces.Update(ctx, item)
		s.audit(ctx, item.OwnerID, "workspace.status_failed", "workspace", item.ID, workspaceAuditDetail(item))
		return nil, errors.Join(err, persistErr)
	}
	now := time.Now().UTC()
	if item.Status == workspacedomain.StatusCreating && item.BranchName == "" {
		item.BranchName = status.Branch
	} else if status.Branch != item.BranchName {
		err = fmt.Errorf("%w: workspace branch changed from %q to %q", agenterrors.ErrConflict, item.BranchName, status.Branch)
		item.HeadSHA, item.Dirty, item.Unpushed, item.LastCheckedAt, item.ErrorMessage = status.Head, true, true, &now, err.Error()
		persistErr := s.workspaces.Update(ctx, item)
		s.audit(ctx, item.OwnerID, "workspace.status_failed", "workspace", item.ID, workspaceAuditDetail(item))
		return nil, errors.Join(err, persistErr)
	}
	item.HeadSHA, item.Dirty, item.Unpushed, item.LastCheckedAt, item.ErrorMessage = status.Head, status.Dirty, status.Unpushed, &now, ""
	if item.Kind == workspacedomain.KindWorktree && item.BaseSHA != "" && item.HeadSHA != "" && item.HeadSHA != item.BaseSHA {
		// A worktree branch without an upstream cannot prove that its commits
		// are published. Treat any commit beyond the recorded base as unpushed.
		item.Unpushed = true
	}
	if item.Kind == workspacedomain.KindWorktree {
		tree, listErr := s.registeredWorktree(ctx, item)
		if listErr != nil {
			item.Dirty, item.Unpushed, item.ErrorMessage = true, true, listErr.Error()
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
	if item.Dirty || item.Unpushed {
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
func (s *Service) GitStatus(ctx context.Context, item *workspacedomain.Workspace) (gitinfra.Status, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return gitinfra.Status{}, err
	}
	if item.Kind == workspacedomain.KindWorktree {
		if _, err := s.registeredWorktree(ctx, item); err != nil {
			return gitinfra.Status{}, err
		}
	}
	status, err := s.git.Status(ctx, item.WorkspacePath)
	if err == nil && item.Kind == workspacedomain.KindWorktree && item.BaseSHA != "" && status.Head != "" && status.Head != item.BaseSHA {
		status.Unpushed = true
	}
	return status, err
}
func (s *Service) ProjectGitStatus(ctx context.Context, item *projectdomain.Project) (gitinfra.Status, error) {
	root, err := s.validateProjectRepository(ctx, item)
	if err != nil {
		return gitinfra.Status{}, err
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
func (s *Service) GitWorktrees(ctx context.Context, item *projectdomain.Project) ([]gitinfra.Worktree, error) {
	root, err := s.validateProjectRepository(ctx, item)
	if err != nil {
		return nil, err
	}
	return s.git.ListWorktrees(ctx, root)
}
func (s *Service) Commit(ctx context.Context, item *workspacedomain.Workspace, message string, paths []string) (gitinfra.CommitResult, error) {
	if err := s.validateWorkspaceProjectBinding(ctx, item); err != nil {
		return gitinfra.CommitResult{}, err
	}
	if item.Kind == workspacedomain.KindWorktree {
		if _, err := s.registeredWorktree(ctx, item); err != nil {
			return gitinfra.CommitResult{}, err
		}
	}
	currentBranch := s.git.CurrentBranch(ctx, item.WorkspacePath)
	if strings.TrimSpace(item.BranchName) == "" || currentBranch != item.BranchName {
		return gitinfra.CommitResult{}, fmt.Errorf("%w: workspace branch changed from %q to %q", agenterrors.ErrConflict, item.BranchName, currentBranch)
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
			_ = s.workspaces.Update(ctx, refreshTarget)
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
		src, err := gitinfra.EnsureSafePath(repositoryRoot, filepath.Join(repositoryRoot, entry))
		if err != nil {
			continue
		}
		dst, err := gitinfra.EnsureSafePath(workspaceRoot, filepath.Join(workspaceRoot, entry))
		if err != nil {
			continue
		}
		resolvedSrc, err := filepath.EvalSymlinks(src)
		if err != nil {
			continue
		}
		if _, err := gitinfra.EnsureInside(repositoryRoot, resolvedSrc); err != nil {
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
		target, err := gitinfra.EnsureSafePath(workspaceRoot, filepath.Join(destination, relative))
		if err != nil {
			return err
		}
		resolved := path
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err = filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if _, err := gitinfra.EnsureInside(repoRoot, resolved); err != nil {
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
