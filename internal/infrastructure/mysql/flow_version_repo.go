package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/agent"

	"gorm.io/gorm"
)

type FlowVersionRepository struct{ db *gorm.DB }

func NewFlowVersionRepository(db *gorm.DB) *FlowVersionRepository {
	return &FlowVersionRepository{db: db}
}

func (r *FlowVersionRepository) Create(ctx context.Context, item *agent.FlowVersion) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *FlowVersionRepository) ListByAgent(ctx context.Context, ownerID, agentID int64) ([]agent.FlowVersion, error) {
	var items []agent.FlowVersion
	err := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ?", ownerID, agentID).Order("version_no DESC").Find(&items).Error
	return items, err
}

func (r *FlowVersionRepository) FindByID(ctx context.Context, ownerID, id int64) (*agent.FlowVersion, error) {
	var item agent.FlowVersion
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *FlowVersionRepository) FindCurrentByAgent(ctx context.Context, ownerID, agentID int64) (*agent.FlowVersion, error) {
	var item agent.FlowVersion
	err := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ? AND is_published = 1", ownerID, agentID).Order("version_no DESC").First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *FlowVersionRepository) FindLatestByAgent(ctx context.Context, ownerID, agentID int64) (*agent.FlowVersion, error) {
	var item agent.FlowVersion
	err := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ?", ownerID, agentID).Order("version_no DESC").First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *FlowVersionRepository) NextVersionNo(ctx context.Context, ownerID, agentID int64) (int, error) {
	var maxVersion int
	err := r.db.WithContext(ctx).Model(&agent.FlowVersion{}).Where("owner_id = ? AND agent_id = ?", ownerID, agentID).Select("COALESCE(MAX(version_no), 0)").Scan(&maxVersion).Error
	return maxVersion + 1, err
}

func (r *FlowVersionRepository) Publish(ctx context.Context, ownerID, agentID, versionID int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&agent.FlowVersion{}).Where("owner_id = ? AND agent_id = ?", ownerID, agentID).Updates(map[string]any{"is_published": false, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&agent.FlowVersion{}).Where("id = ? AND owner_id = ? AND agent_id = ?", versionID, ownerID, agentID).Updates(map[string]any{"is_published": true, "is_draft": false, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&agent.Agent{}).Where("id = ? AND owner_id = ?", agentID, ownerID).Updates(map[string]any{"current_version_id": versionID, "updated_at": now}).Error
	})
}
