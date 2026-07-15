package workspace_usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type Service struct {
	repo         workspace.Repository
	enabled      bool
	allowedRoots []string
	defaultImage string
}

func NewService(repo workspace.Repository, enabled bool, allowedRoots []string, defaultImage string) *Service {
	service := &Service{repo: repo, enabled: enabled, defaultImage: defaultImage}
	for _, root := range allowedRoots {
		if canonical, err := canonicalExistingDir(root); err == nil {
			service.allowedRoots = append(service.allowedRoots, canonical)
		}
	}
	return service
}

type CreateWorkspaceRequest struct {
	Name          string `json:"name"`
	RootPath      string `json:"root_path"`
	DefaultBranch string `json:"default_branch"`
}

func (s *Service) CreateWorkspace(ctx context.Context, ownerID int64, req CreateWorkspaceRequest) (*workspace.Workspace, error) {
	if !s.enabled {
		return nil, fmt.Errorf("%w: workspace runtime is disabled by server configuration", agenterrors.ErrForbidden)
	}
	root, err := canonicalExistingDir(req.RootPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	if !withinAnyRoot(root, s.allowedRoots) {
		return nil, fmt.Errorf("%w: workspace root is outside configured server roots", agenterrors.ErrForbidden)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &workspace.Workspace{OwnerID: ownerID, Name: strings.TrimSpace(req.Name), RootPath: root, DefaultBranch: strings.TrimSpace(req.DefaultBranch), Status: workspace.StatusActive}
	if err := s.repo.CreateWorkspace(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}
func (s *Service) ListWorkspaces(ctx context.Context, ownerID int64) ([]workspace.Workspace, error) {
	return s.repo.ListWorkspaces(ctx, ownerID)
}
func (s *Service) GetWorkspace(ctx context.Context, ownerID, id int64) (*workspace.Workspace, error) {
	item, err := s.repo.FindWorkspace(ctx, ownerID, id)
	if err != nil {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}
func (s *Service) DeleteWorkspace(ctx context.Context, ownerID, id int64) error {
	if _, err := s.GetWorkspace(ctx, ownerID, id); err != nil {
		return err
	}
	return s.repo.DeleteWorkspace(ctx, ownerID, id)
}

type CreatePackRequest struct {
	Name             string   `json:"name"`
	AllowedPaths     []string `json:"allowed_paths"`
	CommandAllowlist []string `json:"command_allowlist"`
	NetworkEnabled   bool     `json:"network_enabled"`
	AllowedDomains   []string `json:"allowed_domains"`
	DockerImage      string   `json:"docker_image"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	CPULimit         string   `json:"cpu_limit"`
	MemoryLimitMB    int      `json:"memory_limit_mb"`
	ProcessLimit     int      `json:"process_limit"`
	MaxOutputBytes   int      `json:"max_output_bytes"`
}

func (s *Service) CreatePack(ctx context.Context, ownerID, workspaceID int64, req CreatePackRequest) (*workspace.Pack, error) {
	if _, err := s.GetWorkspace(ctx, ownerID, workspaceID); err != nil {
		return nil, err
	}
	image := strings.TrimSpace(req.DockerImage)
	if image == "" {
		image = s.defaultImage
	}
	item := &workspace.Pack{OwnerID: ownerID, WorkspaceID: workspaceID, Name: strings.TrimSpace(req.Name), AllowedPaths: req.AllowedPaths, CommandAllowlist: req.CommandAllowlist, NetworkEnabled: req.NetworkEnabled, AllowedDomains: req.AllowedDomains, DockerImage: image, TimeoutSeconds: req.TimeoutSeconds, CPULimit: req.CPULimit, MemoryLimitMB: req.MemoryLimitMB, ProcessLimit: req.ProcessLimit, MaxOutputBytes: req.MaxOutputBytes, Status: workspace.StatusActive}
	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	if err := s.repo.CreatePack(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}
func (s *Service) ListPacks(ctx context.Context, ownerID, workspaceID int64) ([]workspace.Pack, error) {
	if workspaceID > 0 {
		if _, err := s.GetWorkspace(ctx, ownerID, workspaceID); err != nil {
			return nil, err
		}
	}
	return s.repo.ListPacks(ctx, ownerID, workspaceID)
}
func (s *Service) GetPack(ctx context.Context, ownerID, id int64) (*workspace.Pack, error) {
	item, err := s.repo.FindPack(ctx, ownerID, id)
	if err != nil {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}
func (s *Service) DeletePack(ctx context.Context, ownerID, id int64) error {
	if _, err := s.GetPack(ctx, ownerID, id); err != nil {
		return err
	}
	return s.repo.DeletePack(ctx, ownerID, id)
}

func canonicalExistingDir(value string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	return filepath.Clean(resolved), nil
}
func withinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
