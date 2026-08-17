package workspace_usecase

import (
	"context"
	"fmt"
	"strings"

	projectdomain "agentcanvas/internal/domain/project"
	agenterrors "agentcanvas/internal/pkg/errors"
)

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
