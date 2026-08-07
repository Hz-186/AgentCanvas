package mysql

import (
	"context"
	"errors"
	"fmt"
	"time"

	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type WorkspaceRepository struct{ db *gorm.DB }

func NewWorkspaceRepository(db *gorm.DB) *WorkspaceRepository { return &WorkspaceRepository{db: db} }

func (r *WorkspaceRepository) Create(ctx context.Context, item *workspacedomain.Workspace) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return mapMySQLConstraintError(r.db.WithContext(ctx).Create(item).Error)
}

func mapMySQLConstraintError(err error) error {
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return fmt.Errorf("%w: %s", agenterrors.ErrConflict, mysqlErr.Message)
	}
	return err
}

func (r *WorkspaceRepository) FindByID(ctx context.Context, ownerID, id int64) (*workspacedomain.Workspace, error) {
	var item workspacedomain.Workspace
	if err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *WorkspaceRepository) FindByRunID(ctx context.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	var item workspacedomain.Workspace
	if err := r.db.WithContext(ctx).Where("run_id = ? AND owner_id = ?", runID, ownerID).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *WorkspaceRepository) ListByProject(ctx context.Context, ownerID, projectID int64) ([]workspacedomain.Workspace, error) {
	var items []workspacedomain.Workspace
	err := r.db.WithContext(ctx).Where("owner_id = ? AND project_id = ?", ownerID, projectID).Order("id DESC").Find(&items).Error
	return items, err
}

func (r *WorkspaceRepository) ListRecoverable(ctx context.Context, limit int) ([]workspacedomain.Workspace, error) {
	if limit > 1000 {
		limit = 1000
	}
	var items []workspacedomain.Workspace
	query := r.db.WithContext(ctx).
		Where("status IN ?", []string{workspacedomain.StatusReady, workspacedomain.StatusCreating}).
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&items).Error
	return items, err
}

func (r *WorkspaceRepository) Update(ctx context.Context, item *workspacedomain.Workspace) error {
	item.UpdatedAt = time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&workspacedomain.Workspace{}).
		Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).
		Select("*").
		Omit("id", "owner_id", "created_at").
		Updates(item)
	if result.Error != nil {
		return mapMySQLConstraintError(result.Error)
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := r.db.WithContext(ctx).Model(&workspacedomain.Workspace{}).Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return agenterrors.ErrNotFound
		}
	}
	return nil
}

func (r *WorkspaceRepository) ListStale(ctx context.Context, before time.Time, limit int) ([]workspacedomain.Workspace, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var items []workspacedomain.Workspace
	err := r.db.WithContext(ctx).Where("status IN ? AND updated_at < ?", []string{workspacedomain.StatusReady, workspacedomain.StatusCreating}, before).Order("updated_at ASC").Limit(limit).Find(&items).Error
	return items, err
}
