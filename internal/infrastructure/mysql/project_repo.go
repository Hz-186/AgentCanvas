package mysql

import (
	"context"
	"errors"
	"time"

	projectdomain "agentcanvas/internal/domain/project"
	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type ProjectRepository struct{ db *gorm.DB }

func NewProjectRepository(db *gorm.DB) *ProjectRepository { return &ProjectRepository{db: db} }

func (r *ProjectRepository) CreateWithPrimaryFolder(ctx context.Context, item *projectdomain.Project, folder *projectdomain.ProjectFolder) error {
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	folder.AddedAt = now
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		folder.OwnerID = item.OwnerID
		folder.ProjectID = item.ID
		folder.Path = item.RepositoryRoot
		folder.IsRepositoryRoot = true
		return tx.Create(folder).Error
	})
	return mapMySQLConstraintError(err)
}

func (r *ProjectRepository) ListByOwner(ctx context.Context, ownerID int64, includeArchived bool) ([]projectdomain.Project, error) {
	var items []projectdomain.Project
	q := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if !includeArchived {
		q = q.Where("archived = 0")
	}
	if err := q.Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		folders, err := r.ListFolders(ctx, ownerID, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].Folders = folders
	}
	return items, nil
}

func (r *ProjectRepository) FindByID(ctx context.Context, ownerID, id int64) (*projectdomain.Project, error) {
	var item projectdomain.Project
	if err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	folders, err := r.ListFolders(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	item.Folders = folders
	return &item, nil
}

func (r *ProjectRepository) Update(ctx context.Context, item *projectdomain.Project) error {
	item.UpdatedAt = time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&projectdomain.Project{}).
		Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).
		Select("*").
		Omit("id", "owner_id", "created_at").
		Updates(item)
	if result.Error != nil {
		return mapMySQLConstraintError(result.Error)
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := r.db.WithContext(ctx).Model(&projectdomain.Project{}).Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return agenterrors.ErrNotFound
		}
	}
	return nil
}

func (r *ProjectRepository) Archive(ctx context.Context, ownerID, id int64) error {
	result := r.db.WithContext(ctx).Model(&projectdomain.Project{}).Where("id = ? AND owner_id = ?", id, ownerID).Updates(map[string]any{"archived": true, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return agenterrors.ErrNotFound
	}
	return nil
}

func (r *ProjectRepository) ListFolders(ctx context.Context, ownerID, projectID int64) ([]projectdomain.ProjectFolder, error) {
	var items []projectdomain.ProjectFolder
	err := r.db.WithContext(ctx).Where("owner_id = ? AND project_id = ?", ownerID, projectID).Order("is_repository_root DESC, id ASC").Find(&items).Error
	return items, err
}

func (r *ProjectRepository) AddFolder(ctx context.Context, item *projectdomain.ProjectFolder) error {
	if item.AddedAt.IsZero() {
		item.AddedAt = time.Now().UTC()
	}
	return mapMySQLConstraintError(r.db.WithContext(ctx).Create(item).Error)
}

func (r *ProjectRepository) AddPrimaryFolder(ctx context.Context, item *projectdomain.ProjectFolder) error {
	if item.AddedAt.IsZero() {
		item.AddedAt = time.Now().UTC()
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		if err := tx.Model(&projectdomain.ProjectFolder{}).
			Where("owner_id = ? AND project_id = ? AND id <> ?", item.OwnerID, item.ProjectID, item.ID).
			Update("is_repository_root", false).Error; err != nil {
			return err
		}
		result := tx.Model(&projectdomain.Project{}).
			Where("id = ? AND owner_id = ?", item.ProjectID, item.OwnerID).
			Update("repository_root", item.Path)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return agenterrors.ErrNotFound
		}
		return nil
	})
	return mapMySQLConstraintError(err)
}

func (r *ProjectRepository) DeleteFolder(ctx context.Context, ownerID, projectID, folderID int64) error {
	result := r.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND project_id = ?", folderID, ownerID, projectID).Delete(&projectdomain.ProjectFolder{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return agenterrors.ErrNotFound
	}
	return nil
}
