package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workspace"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkspaceRepository struct{ db *gorm.DB }

func NewWorkspaceRepository(db *gorm.DB) *WorkspaceRepository { return &WorkspaceRepository{db: db} }

func (r *WorkspaceRepository) CreateWorkspace(ctx context.Context, item *workspace.Workspace) error {
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if item.Status == "" {
		item.Status = workspace.StatusActive
	}
	return r.db.WithContext(ctx).Create(item).Error
}
func (r *WorkspaceRepository) ListWorkspaces(ctx context.Context, ownerID int64) ([]workspace.Workspace, error) {
	var items []workspace.Workspace
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).Order("updated_at DESC,id DESC").Find(&items).Error
	return items, err
}
func (r *WorkspaceRepository) FindWorkspace(ctx context.Context, ownerID, id int64) (*workspace.Workspace, error) {
	var item workspace.Workspace
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}
func (r *WorkspaceRepository) UpdateWorkspace(ctx context.Context, item *workspace.Workspace) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}
func (r *WorkspaceRepository) DeleteWorkspace(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&workspace.Workspace{}).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).Updates(map[string]any{"deleted_at": now, "status": workspace.StatusDisabled, "updated_at": now}).Error
}

func (r *WorkspaceRepository) CreatePack(ctx context.Context, item *workspace.Pack) error {
	if err := item.Encode(); err != nil {
		return err
	}
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	return r.db.WithContext(ctx).Create(item).Error
}
func (r *WorkspaceRepository) ListPacks(ctx context.Context, ownerID, workspaceID int64) ([]workspace.Pack, error) {
	var items []workspace.Pack
	q := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	if workspaceID > 0 {
		q = q.Where("workspace_id = ?", workspaceID)
	}
	err := q.Order("updated_at DESC,id DESC").Find(&items).Error
	for i := range items {
		items[i].Decode()
	}
	return items, err
}
func (r *WorkspaceRepository) FindPack(ctx context.Context, ownerID, id int64) (*workspace.Pack, error) {
	var item workspace.Pack
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error; err != nil {
		return nil, err
	}
	item.Decode()
	return &item, nil
}
func (r *WorkspaceRepository) UpdatePack(ctx context.Context, item *workspace.Pack) error {
	if err := item.Encode(); err != nil {
		return err
	}
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}
func (r *WorkspaceRepository) DeletePack(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&workspace.Pack{}).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).Updates(map[string]any{"deleted_at": now, "status": workspace.StatusDisabled, "updated_at": now}).Error
}

func (r *WorkspaceRepository) AcquireRunLease(ctx context.Context, requested *workspace.RunLease) (*workspace.RunLease, error) {
	var acquired workspace.RunLease
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND workspace_id = ? AND run_id = ?", requested.OwnerID, requested.WorkspaceID, requested.RunID).First(&acquired).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		now := time.Now().UTC()
		if err == gorm.ErrRecordNotFound {
			acquired = *requested
			acquired.Status = workspace.StatusActive
			acquired.CreatedAt, acquired.UpdatedAt = now, now
			return tx.Create(&acquired).Error
		}
		if acquired.WorktreePath == "" {
			acquired.WorktreePath = requested.WorktreePath
		}
		acquired.LeaseToken = requested.LeaseToken
		acquired.LeaseExpiresAt = requested.LeaseExpiresAt
		acquired.Status = workspace.StatusActive
		acquired.UpdatedAt = now
		return tx.Save(&acquired).Error
	})
	return &acquired, err
}

func (r *WorkspaceRepository) HeartbeatRunLease(ctx context.Context, id int64, token string, expiresAt time.Time) (bool, error) {
	result := r.db.WithContext(ctx).Model(&workspace.RunLease{}).Where("id = ? AND lease_token = ? AND status = ?", id, token, workspace.StatusActive).Updates(map[string]any{"lease_expires_at": expiresAt.UTC(), "updated_at": time.Now().UTC()})
	return result.RowsAffected == 1, result.Error
}

func (r *WorkspaceRepository) ReleaseRunLease(ctx context.Context, id int64, token string) (bool, error) {
	result := r.db.WithContext(ctx).Model(&workspace.RunLease{}).Where("id = ? AND lease_token = ? AND status = ?", id, token, workspace.StatusActive).Updates(map[string]any{"status": workspace.StatusReleased, "updated_at": time.Now().UTC()})
	return result.RowsAffected == 1, result.Error
}

func (r *WorkspaceRepository) ListExpiredRunLeases(ctx context.Context, before time.Time, limit int) ([]workspace.RunLease, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var items []workspace.RunLease
	err := r.db.WithContext(ctx).Where("status = ? AND lease_expires_at < ?", workspace.StatusActive, before.UTC()).Order("lease_expires_at ASC").Limit(limit).Find(&items).Error
	return items, err
}

var _ workspace.Repository = (*WorkspaceRepository)(nil)
