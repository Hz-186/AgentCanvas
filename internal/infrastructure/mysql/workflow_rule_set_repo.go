package mysql

import (
	"context"
	"fmt"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkflowRuleSetRepository struct{ db *gorm.DB }

func NewWorkflowRuleSetRepository(db *gorm.DB) *WorkflowRuleSetRepository {
	return &WorkflowRuleSetRepository{db: db}
}

func (r *WorkflowRuleSetRepository) CreateDraft(ctx context.Context, item *workflow.RuleSet, nodes []workflow.RuleNode) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if item.VersionNo <= 0 {
			var latest int
			if err := tx.Model(&workflow.RuleSet{}).Where("owner_id = ? AND workflow_id = ?", item.OwnerID, item.WorkflowID).
				Select("COALESCE(MAX(version_no), 0)").Scan(&latest).Error; err != nil {
				return err
			}
			item.VersionNo = latest + 1
		}
		item.Status = workflow.RuleSetStatusDraft
		item.Revision = 1
		item.CreatedAt = time.Now().UTC()
		item.UpdatedAt = item.CreatedAt
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return replaceRuleSetChildren(tx, item.ID, nodes)
	})
}

func (r *WorkflowRuleSetRepository) ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]workflow.RuleSet, error) {
	var items []workflow.RuleSet
	err := r.db.WithContext(ctx).Where("owner_id = ? AND workflow_id = ?", ownerID, workflowID).
		Order("version_no DESC").Find(&items).Error
	return items, err
}

func (r *WorkflowRuleSetRepository) FindByID(ctx context.Context, ownerID, workflowID, id int64) (*workflow.RuleSet, error) {
	var item workflow.RuleSet
	if err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND workflow_id = ?", id, ownerID, workflowID).First(&item).Error; err != nil {
		return nil, err
	}
	if err := loadRuleSetChildren(r.db.WithContext(ctx), &item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowRuleSetRepository) UpdateDraft(ctx context.Context, item *workflow.RuleSet, nodes []workflow.RuleNode, expectedRevision int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ? AND workflow_id = ?", item.ID, item.OwnerID, item.WorkflowID).First(&stored).Error; err != nil {
			return err
		}
		if stored.Status == workflow.RuleSetStatusPublished || stored.Status == workflow.RuleSetStatusSuperseded {
			return workflow.ErrRuleSetImmutable
		}
		if stored.Status != workflow.RuleSetStatusDraft || stored.Revision != expectedRevision {
			return workflow.ErrRuleSetConflict
		}
		stored.Revision++
		stored.SourceHash = ""
		stored.CompiledHash = ""
		stored.CompiledSnapshotJSON = nil
		stored.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		if err := replaceRuleSetChildren(tx, stored.ID, nodes); err != nil {
			return err
		}
		*item = stored
		item.Nodes = append([]workflow.RuleNode(nil), nodes...)
		return nil
	})
}

func (r *WorkflowRuleSetRepository) Publish(ctx context.Context, item *workflow.RuleSet, nodes []workflow.RuleNode, compiledSnapshot []byte, compiledHash, tokenEstimator string, publishedBy, expectedRevision int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ? AND workflow_id = ?", item.ID, item.OwnerID, item.WorkflowID).First(&stored).Error; err != nil {
			return err
		}
		if stored.Status == workflow.RuleSetStatusPublished && stored.Revision == expectedRevision {
			*item = stored
			return nil
		}
		if stored.Status != workflow.RuleSetStatusDraft || stored.Revision != expectedRevision {
			return workflow.ErrRuleSetConflict
		}
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ? AND status = ? AND id <> ?", stored.OwnerID, stored.WorkflowID, workflow.RuleSetStatusPublished, stored.ID).
			Update("status", workflow.RuleSetStatusSuperseded).Error; err != nil {
			return err
		}
		if err := replaceRuleSetChildren(tx, stored.ID, nodes); err != nil {
			return err
		}
		now := time.Now().UTC()
		stored.Status = workflow.RuleSetStatusPublished
		stored.SourceHash = item.SourceHash
		stored.CompiledSnapshotJSON = append([]byte(nil), compiledSnapshot...)
		stored.CompiledHash = compiledHash
		stored.TokenEstimatorVersion = tokenEstimator
		stored.PublishedBy = &publishedBy
		stored.PublishedAt = &now
		stored.UpdatedAt = now
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		updated := tx.Model(&workflow.Profile{}).Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", stored.OwnerID, stored.WorkflowID).
			Update("active_rule_set_id", stored.ID)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("workflow profile not found while publishing rule set")
		}
		*item = stored
		return nil
	})
}

func (r *WorkflowRuleSetRepository) RollbackPublished(ctx context.Context, target *workflow.RuleSet, clone *workflow.RuleSet, publishedBy int64, compile workflow.RuleSetRollbackCompiler) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ? AND workflow_id = ?", target.ID, target.OwnerID, target.WorkflowID).First(&stored).Error; err != nil {
			return err
		}
		if stored.Status != workflow.RuleSetStatusPublished && stored.Status != workflow.RuleSetStatusSuperseded {
			return fmt.Errorf("%w: rollback target status is %s", workflow.ErrRuleSetConflict, stored.Status)
		}
		var latest int
		if err := tx.Model(&workflow.RuleSet{}).Where("owner_id = ? AND workflow_id = ?", stored.OwnerID, stored.WorkflowID).
			Select("COALESCE(MAX(version_no), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		rollbackID := stored.ID
		*clone = stored
		clone.ID = 0
		clone.VersionNo = latest + 1
		clone.Status = workflow.RuleSetStatusPublished
		clone.Revision = 1
		clone.RollbackOfRuleSetID = &rollbackID
		clone.PublishedBy = &publishedBy
		clone.PublishedAt = &now
		clone.CreatedAt = now
		clone.UpdatedAt = now
		clone.Nodes = nil
		clone.CompiledSnapshotJSON = nil
		clone.CompiledHash = ""
		if err := tx.Create(clone).Error; err != nil {
			return err
		}
		if compile == nil {
			return fmt.Errorf("rollback compiler is not configured")
		}
		nodes, snapshot, compiledHash, tokenEstimator, err := compile(clone.ID, clone.VersionNo)
		if err != nil {
			return err
		}
		clone.CompiledSnapshotJSON = append([]byte(nil), snapshot...)
		clone.CompiledHash = compiledHash
		clone.TokenEstimatorVersion = tokenEstimator
		if err := tx.Save(clone).Error; err != nil {
			return err
		}
		if err := replaceRuleSetChildren(tx, clone.ID, nodes); err != nil {
			return err
		}
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ? AND status = ? AND id <> ?", stored.OwnerID, stored.WorkflowID, workflow.RuleSetStatusPublished, clone.ID).
			Update("status", workflow.RuleSetStatusSuperseded).Error; err != nil {
			return err
		}
		updated := tx.Model(&workflow.Profile{}).Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", stored.OwnerID, stored.WorkflowID).
			Update("active_rule_set_id", clone.ID)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("workflow profile not found while rolling back rule set")
		}
		return nil
	})
}

func replaceRuleSetChildren(tx *gorm.DB, ruleSetID int64, nodes []workflow.RuleNode) error {
	if err := tx.Where("rule_set_id = ?", ruleSetID).Delete(&workflow.RuleNode{}).Error; err != nil {
		return err
	}
	for index := range nodes {
		nodes[index].ID = 0
		nodes[index].RuleSetID = ruleSetID
	}
	if len(nodes) > 0 {
		return tx.Create(&nodes).Error
	}
	return nil
}

func loadRuleSetChildren(db *gorm.DB, item *workflow.RuleSet) error {
	return db.Where("rule_set_id = ?", item.ID).Order("rule_id ASC").Find(&item.Nodes).Error
}

var _ workflow.RuleSetRepository = (*WorkflowRuleSetRepository)(nil)
