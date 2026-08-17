package workspace_usecase

import (
	"context"
	"strconv"
	"time"

	"agentcanvas/internal/domain/audit"
	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
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
	git        GitService
	audits     audit.Repository
	cfg        Config
}

type GitService interface {
	RepoRoot(context.Context, string) (string, error)
	EnsureRepository(context.Context, string, bool) (string, error)
	Head(context.Context, string) (string, error)
	ResolveCommit(context.Context, string, string) (string, error)
	CurrentBranch(context.Context, string) string
	ListWorktrees(context.Context, string) ([]workspacedomain.GitWorktree, error)
	ResolveBase(context.Context, string) (string, string)
	AddWorktree(context.Context, string, string, string, string) error
	LockWorktree(context.Context, string, string, string) error
	UnlockWorktree(context.Context, string, string) error
	RemoveWorktree(context.Context, string, string, bool) error
	Status(context.Context, string) (workspacedomain.GitStatus, error)
	Diff(context.Context, string, bool) (string, error)
	Log(context.Context, string, int) (string, error)
	Branches(context.Context, string) ([]string, error)
	Commit(context.Context, string, string, []string) (workspacedomain.GitCommitResult, error)
}

func (s *Service) ConfigureAudits(repository audit.Repository) { s.audits = repository }

func (s *Service) audit(ctx context.Context, ownerID int64, action, resourceType string, resourceID int64, detail map[string]any) {
	if s.audits == nil || ownerID <= 0 {
		return
	}
	_ = s.audits.Create(ctx, audit.NewLog(ownerID, ownerID, action, resourceType, strconv.FormatInt(resourceID, 10), detail, "", ""))
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

func NewService(projects projectdomain.Repository, workspaces workspacedomain.Repository, gitService GitService, cfg Config) *Service {
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
