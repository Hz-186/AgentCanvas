package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/tool"

	"gorm.io/gorm"
)

type ToolDefinitionRepository struct{ db *gorm.DB }

func NewToolDefinitionRepository(db *gorm.DB) *ToolDefinitionRepository {
	return &ToolDefinitionRepository{db: db}
}

func (r *ToolDefinitionRepository) Create(ctx context.Context, item *tool.Definition) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return enqueueContextResource(ctx, tx, item.OwnerID, 0, contextresource.TypeTool, item.ID, contextresource.OperationUpsert, toolContextText(*item))
	})
}

func (r *ToolDefinitionRepository) Update(ctx context.Context, item *tool.Definition) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(item).Error; err != nil {
			return err
		}
		return enqueueContextResource(ctx, tx, item.OwnerID, 0, contextresource.TypeTool, item.ID, contextresource.OperationUpsert, toolContextText(*item))
	})
}

func (r *ToolDefinitionRepository) FindByID(ctx context.Context, ownerID, id int64) (*tool.Definition, error) {
	var item tool.Definition
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ToolDefinitionRepository) List(ctx context.Context, ownerID int64, limit, offset int) ([]tool.Definition, error) {
	var items []tool.Definition
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *ToolDefinitionRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&tool.Definition{}).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
			Updates(map[string]any{"deleted_at": now, "updated_at": now})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return enqueueContextResource(ctx, tx, ownerID, 0, contextresource.TypeTool, id, contextresource.OperationDelete, "")
	})
}

type ToolInvocationRepository struct{ db *gorm.DB }

func NewToolInvocationRepository(db *gorm.DB) *ToolInvocationRepository {
	return &ToolInvocationRepository{db: db}
}

func (r *ToolInvocationRepository) Create(ctx context.Context, item *tool.Invocation) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ToolInvocationRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]tool.Invocation, error) {
	var items []tool.Invocation
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}

type ToolPolicyRepository struct{ db *gorm.DB }

func NewToolPolicyRepository(db *gorm.DB) *ToolPolicyRepository {
	return &ToolPolicyRepository{db: db}
}

func (r *ToolPolicyRepository) Create(ctx context.Context, item *tool.ToolPolicy) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ToolPolicyRepository) FindByID(ctx context.Context, ownerID, id int64) (*tool.ToolPolicy, error) {
	var item tool.ToolPolicy
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ToolPolicyRepository) List(ctx context.Context, ownerID int64) ([]tool.ToolPolicy, error) {
	var items []tool.ToolPolicy
	err := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *ToolPolicyRepository) Update(ctx context.Context, item *tool.ToolPolicy) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ToolPolicyRepository) Delete(ctx context.Context, ownerID, id int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).Delete(&tool.ToolPolicy{}).Error
}

type ToolPackRepository struct{ db *gorm.DB }

func NewToolPackRepository(db *gorm.DB) *ToolPackRepository {
	return &ToolPackRepository{db: db}
}

func (r *ToolPackRepository) CreatePack(ctx context.Context, item *tool.ToolPack) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ToolPackRepository) FindPackByID(ctx context.Context, ownerID, id int64) (*tool.ToolPack, error) {
	var item tool.ToolPack
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *ToolPackRepository) ListPacks(ctx context.Context, ownerID int64) ([]tool.ToolPack, error) {
	var items []tool.ToolPack
	err := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *ToolPackRepository) UpdatePack(ctx context.Context, item *tool.ToolPack) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *ToolPackRepository) DeletePack(ctx context.Context, ownerID, id int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).Delete(&tool.ToolPack{}).Error
}

func (r *ToolPackRepository) AddItem(ctx context.Context, item *tool.ToolPackItem) error {
	item.CreatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *ToolPackRepository) RemoveItem(ctx context.Context, ownerID, packID, toolID int64) error {
	return r.db.WithContext(ctx).Where("owner_id = ? AND pack_id = ? AND tool_id = ?", ownerID, packID, toolID).Delete(&tool.ToolPackItem{}).Error
}

func (r *ToolPackRepository) ListItems(ctx context.Context, ownerID, packID int64) ([]tool.ToolPackItem, error) {
	var items []tool.ToolPackItem
	err := r.db.WithContext(ctx).Where("owner_id = ? AND pack_id = ?", ownerID, packID).Order("id ASC").Find(&items).Error
	return items, err
}

func (r *ToolPackRepository) ListToolIDs(ctx context.Context, ownerID, packID int64) ([]int64, error) {
	items, err := r.ListItems(ctx, ownerID, packID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = item.ToolID
	}
	return ids, nil
}

type MCPRepository struct{ db *gorm.DB }

func NewMCPRepository(db *gorm.DB) *MCPRepository {
	return &MCPRepository{db: db}
}

func (r *MCPRepository) CreateServer(ctx context.Context, item *tool.MCPServer) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Status == 0 {
		item.Status = tool.MCPStatusActive
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *MCPRepository) FindServerByID(ctx context.Context, ownerID, id int64) (*tool.MCPServer, error) {
	var item tool.MCPServer
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *MCPRepository) ListServers(ctx context.Context, ownerID int64) ([]tool.MCPServer, error) {
	var items []tool.MCPServer
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *MCPRepository) UpdateServer(ctx context.Context, item *tool.MCPServer) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

func (r *MCPRepository) DeleteServer(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&tool.MCPServer{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (r *MCPRepository) ReplaceToolCache(ctx context.Context, ownerID, serverID int64, tools []tool.MCPToolCache) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? AND server_id = ?", ownerID, serverID).Delete(&tool.MCPToolCache{}).Error; err != nil {
			return err
		}
		if len(tools) == 0 {
			return nil
		}
		now := time.Now().UTC()
		for i := range tools {
			tools[i].OwnerID = ownerID
			tools[i].ServerID = serverID
			tools[i].CachedAt = now
			tools[i].CreatedAt = now
			tools[i].UpdatedAt = now
		}
		return tx.Create(&tools).Error
	})
}

func (r *MCPRepository) ListToolCache(ctx context.Context, ownerID, serverID int64) ([]tool.MCPToolCache, error) {
	var items []tool.MCPToolCache
	err := r.db.WithContext(ctx).Where("owner_id = ? AND server_id = ?", ownerID, serverID).Order("tool_name ASC").Find(&items).Error
	return items, err
}
