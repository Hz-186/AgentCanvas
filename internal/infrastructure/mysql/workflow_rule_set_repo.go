package mysql

import (
	"context"
	"errors"
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

func (r *WorkflowRuleSetRepository) CreateDraft(ctx context.Context, item *workflow.RuleSet, nodes []workflow.RuleNode, edges []workflow.RuleEdge) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if item.VersionNo <= 0 {
			var latest int
			if err := tx.Model(&workflow.RuleSet{}).
				Where("owner_id = ? AND workflow_id = ?", item.OwnerID, item.WorkflowID).
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
		return replaceRuleSetChildren(tx, item.ID, nodes, edges)
	})
}

func (r *WorkflowRuleSetRepository) ListByWorkflow(ctx context.Context, ownerID, workflowID int64) ([]workflow.RuleSet, error) {
	var items []workflow.RuleSet
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND workflow_id = ?", ownerID, workflowID).
		Order("version_no DESC").Find(&items).Error
	return items, err
}

func (r *WorkflowRuleSetRepository) FindByID(ctx context.Context, ownerID, workflowID, id int64) (*workflow.RuleSet, error) {
	var item workflow.RuleSet
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND workflow_id = ?", id, ownerID, workflowID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	if err := loadRuleSetChildren(r.db.WithContext(ctx), &item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *WorkflowRuleSetRepository) UpdateDraft(ctx context.Context, item *workflow.RuleSet, nodes []workflow.RuleNode, edges []workflow.RuleEdge, expectedRevision int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND workflow_id = ?", item.ID, item.OwnerID, item.WorkflowID).
			First(&stored).Error; err != nil {
			return err
		}
		if stored.Status == workflow.RuleSetStatusPublished || stored.Status == workflow.RuleSetStatusSuperseded {
			return workflow.ErrRuleSetImmutable
		}
		if stored.Status != workflow.RuleSetStatusDraft {
			return workflow.ErrRuleSetConflict
		}
		if stored.Revision != expectedRevision {
			return workflow.ErrRuleSetConflict
		}
		stored.Revision++
		stored.Status = workflow.RuleSetStatusDraft
		stored.SourceHash = ""
		stored.CompiledHash = ""
		stored.CompiledSnapshotJSON = nil
		stored.CompileError = ""
		stored.CompilerProviderID = item.CompilerProviderID
		stored.CompilerModel = item.CompilerModel
		stored.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		if err := replaceRuleSetChildren(tx, stored.ID, nodes, edges); err != nil {
			return err
		}
		*item = stored
		item.Nodes = append([]workflow.RuleNode(nil), nodes...)
		item.Edges = append([]workflow.RuleEdge(nil), edges...)
		return nil
	})
}

func (r *WorkflowRuleSetRepository) QueueCompilation(ctx context.Context, item *workflow.RuleSet, job *workflow.RuleCompileJob, expectedRevision int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if job.IdempotencyKey != "" {
			var existing workflow.RuleCompileJob
			err := tx.Where("owner_id = ? AND workflow_id = ? AND idempotency_key = ?", job.OwnerID, job.WorkflowID, job.IdempotencyKey).First(&existing).Error
			if err == nil {
				if existing.RuleSetID != item.ID || existing.Revision != expectedRevision {
					return workflow.ErrRuleSetConflict
				}
				*job = existing
				return nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var stored workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND workflow_id = ?", item.ID, item.OwnerID, item.WorkflowID).
			First(&stored).Error; err != nil {
			return err
		}
		if stored.Status == workflow.RuleSetStatusPublished || stored.Status == workflow.RuleSetStatusSuperseded {
			return workflow.ErrRuleSetImmutable
		}
		if stored.Status != workflow.RuleSetStatusDraft {
			return workflow.ErrRuleSetConflict
		}
		if stored.Revision != expectedRevision {
			return workflow.ErrRuleSetConflict
		}
		stored.Status = workflow.RuleSetStatusQueued
		stored.SourceHash = item.SourceHash
		stored.CompilerProviderID = item.CompilerProviderID
		stored.CompilerModel = item.CompilerModel
		stored.CompileError = ""
		stored.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		job.RuleSetID = stored.ID
		job.Revision = stored.Revision
		job.SourceHash = stored.SourceHash
		job.Status = workflow.RuleCompileJobQueued
		job.AvailableAt = time.Now().UTC()
		job.CreatedAt = job.AvailableAt
		job.UpdatedAt = job.AvailableAt
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		*item = stored
		return nil
	})
}

func (r *WorkflowRuleSetRepository) FindCompileJob(ctx context.Context, ownerID, workflowID, id int64) (*workflow.RuleCompileJob, error) {
	var item workflow.RuleCompileJob
	err := r.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND workflow_id = ?", id, ownerID, workflowID).First(&item).Error
	return &item, err
}

func (r *WorkflowRuleSetRepository) ClaimCompileJob(ctx context.Context, jobID int64, workerID string) (*workflow.RuleCompileJob, error) {
	return r.claimCompileJob(ctx, jobID, workerID)
}

func (r *WorkflowRuleSetRepository) ClaimNextCompileJob(ctx context.Context, workerID string) (*workflow.RuleCompileJob, error) {
	return r.claimCompileJob(ctx, 0, workerID)
}

func (r *WorkflowRuleSetRepository) claimCompileJob(ctx context.Context, jobID int64, workerID string) (*workflow.RuleCompileJob, error) {
	var claimed workflow.RuleCompileJob
	stale := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND available_at <= ? AND attempts < 3", workflow.RuleCompileJobQueued, time.Now().UTC())
		if jobID > 0 {
			query = query.Where("id = ?", jobID)
		}
		if err := query.Order("id ASC").First(&claimed).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		claimed.Status = workflow.RuleCompileJobCompiling
		claimed.Attempts++
		claimed.WorkerID = workerID
		claimed.StartedAt = &now
		claimed.UpdatedAt = now
		if err := tx.Save(&claimed).Error; err != nil {
			return err
		}
		updated := tx.Model(&workflow.RuleSet{}).
			Where("id = ? AND revision = ? AND source_hash = ? AND status IN ?", claimed.RuleSetID, claimed.Revision, claimed.SourceHash, []string{workflow.RuleSetStatusQueued, workflow.RuleSetStatusCompiling}).
			Update("status", workflow.RuleSetStatusCompiling)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			stale = true
			now := time.Now().UTC()
			claimed.Status = workflow.RuleCompileJobStale
			claimed.ErrorMessage = workflow.ErrRuleCompileStale.Error()
			claimed.FinishedAt = &now
			claimed.UpdatedAt = now
			return tx.Save(&claimed).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if stale {
		return nil, workflow.ErrRuleCompileStale
	}
	return &claimed, nil
}

func (r *WorkflowRuleSetRepository) CompleteCompilation(ctx context.Context, job *workflow.RuleCompileJob, nodes []workflow.RuleNode, suggestions []workflow.RuleEdge, compiledSnapshot []byte, compiledHash, tokenEstimator, nextStatus string) error {
	stale := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var storedJob workflow.RuleCompileJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&storedJob, job.ID).Error; err != nil {
			return err
		}
		var set workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&set, storedJob.RuleSetID).Error; err != nil {
			return err
		}
		if set.Revision != storedJob.Revision || set.SourceHash != storedJob.SourceHash || set.Status != workflow.RuleSetStatusCompiling {
			stale = true
			now := time.Now().UTC()
			storedJob.Status = workflow.RuleCompileJobStale
			storedJob.FinishedAt = &now
			storedJob.ErrorMessage = workflow.ErrRuleCompileStale.Error()
			return tx.Save(&storedJob).Error
		}
		if err := tx.Where("rule_set_id = ? AND source = ?", set.ID, "llm").Delete(&workflow.RuleEdge{}).Error; err != nil {
			return err
		}
		for index := range suggestions {
			suggestions[index].ID = 0
			suggestions[index].RuleSetID = set.ID
			suggestions[index].Source = "llm"
			if suggestions[index].Decision == "" {
				suggestions[index].Decision = workflow.RuleEdgeDecisionPending
			}
		}
		if len(suggestions) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&suggestions).Error; err != nil {
				return err
			}
		}
		if len(nodes) > 0 {
			for _, node := range nodes {
				if err := tx.Model(&workflow.RuleNode{}).
					Where("rule_set_id = ? AND rule_id = ?", set.ID, node.RuleID).
					Updates(map[string]any{"token_cost": node.TokenCost, "topological_order": node.TopologicalOrder, "content_hash": node.ContentHash}).Error; err != nil {
					return err
				}
			}
		}
		set.Status = nextStatus
		set.CompiledSnapshotJSON = append([]byte(nil), compiledSnapshot...)
		set.CompiledHash = compiledHash
		set.TokenEstimatorVersion = tokenEstimator
		set.CompilerPromptVersion = "rule-graph-v1"
		set.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&set).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		storedJob.Status = workflow.RuleCompileJobCompleted
		storedJob.PromptTokens = job.PromptTokens
		storedJob.CompletionTokens = job.CompletionTokens
		storedJob.ErrorMessage = ""
		storedJob.FinishedAt = &now
		storedJob.UpdatedAt = now
		return tx.Save(&storedJob).Error
	})
	if err != nil {
		return err
	}
	if stale {
		return workflow.ErrRuleCompileStale
	}
	return nil
}

func (r *WorkflowRuleSetRepository) FailCompilation(ctx context.Context, job *workflow.RuleCompileJob, cause error, retryAt *time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored workflow.RuleCompileJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stored, job.ID).Error; err != nil {
			return err
		}
		message := "rule compilation failed"
		if cause != nil {
			message = cause.Error()
		}
		stored.ErrorMessage = message
		stored.UpdatedAt = time.Now().UTC()
		if retryAt != nil && stored.Attempts < 3 {
			stored.Status = workflow.RuleCompileJobQueued
			stored.AvailableAt = retryAt.UTC()
			stored.WorkerID = ""
			stored.StartedAt = nil
		} else {
			now := time.Now().UTC()
			stored.Status = workflow.RuleCompileJobFailed
			stored.FinishedAt = &now
			if err := tx.Model(&workflow.RuleSet{}).
				Where("id = ? AND revision = ? AND source_hash = ?", stored.RuleSetID, stored.Revision, stored.SourceHash).
				Updates(map[string]any{"status": workflow.RuleSetStatusFailed, "compile_error": message}).Error; err != nil {
				return err
			}
		}
		return tx.Save(&stored).Error
	})
}

func (r *WorkflowRuleSetRepository) UpdateEdgeDecisions(ctx context.Context, ownerID, workflowID, ruleSetID, expectedRevision int64, decisions map[int64]string) (*workflow.RuleSet, error) {
	var result workflow.RuleSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND workflow_id = ?", ruleSetID, ownerID, workflowID).
			First(&result).Error; err != nil {
			return err
		}
		if result.Status != workflow.RuleSetStatusReviewRequired {
			return fmt.Errorf("%w: rule set is not awaiting review", workflow.ErrRuleSetConflict)
		}
		if result.Revision != expectedRevision {
			return workflow.ErrRuleSetConflict
		}
		for edgeID, decision := range decisions {
			if decision != workflow.RuleEdgeDecisionAccepted && decision != workflow.RuleEdgeDecisionRejected {
				return fmt.Errorf("invalid rule edge decision %q", decision)
			}
			updated := tx.Model(&workflow.RuleEdge{}).
				Where("id = ? AND rule_set_id = ? AND decision = ?", edgeID, ruleSetID, workflow.RuleEdgeDecisionPending).
				Update("decision", decision)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return workflow.ErrRuleSetConflict
			}
		}
		var pending int64
		if err := tx.Model(&workflow.RuleEdge{}).Where("rule_set_id = ? AND decision = ?", ruleSetID, workflow.RuleEdgeDecisionPending).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return fmt.Errorf("%w: all pending edges must be reviewed", workflow.ErrRuleSetConflict)
		}
		result.Revision++
		result.Status = workflow.RuleSetStatusReady
		result.UpdatedAt = time.Now().UTC()
		return tx.Save(&result).Error
	})
	if err != nil {
		return nil, err
	}
	if err := loadRuleSetChildren(r.db.WithContext(ctx), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *WorkflowRuleSetRepository) PublishCompiled(ctx context.Context, item *workflow.RuleSet, nodes []workflow.RuleNode, edges []workflow.RuleEdge, compiledSnapshot []byte, compiledHash, tokenEstimator string, publishedBy int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored workflow.RuleSet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND workflow_id = ?", item.ID, item.OwnerID, item.WorkflowID).
			First(&stored).Error; err != nil {
			return err
		}
		if stored.Status != workflow.RuleSetStatusReady {
			return fmt.Errorf("%w: rule set status is %s", workflow.ErrRuleSetConflict, stored.Status)
		}
		if stored.Revision != item.Revision || stored.SourceHash != item.SourceHash {
			return workflow.ErrRuleSetConflict
		}
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ? AND status = ? AND id <> ?", stored.OwnerID, stored.WorkflowID, workflow.RuleSetStatusPublished, stored.ID).
			Update("status", workflow.RuleSetStatusSuperseded).Error; err != nil {
			return err
		}
		if len(nodes) > 0 || len(edges) > 0 {
			if err := replaceRuleSetChildren(tx, stored.ID, nodes, edges); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		stored.Status = workflow.RuleSetStatusPublished
		stored.CompiledSnapshotJSON = append([]byte(nil), compiledSnapshot...)
		stored.CompiledHash = compiledHash
		stored.TokenEstimatorVersion = tokenEstimator
		stored.PublishedBy = &publishedBy
		stored.PublishedAt = &now
		stored.CompileError = ""
		stored.UpdatedAt = now
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		updated := tx.Model(&workflow.Profile{}).
			Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", stored.OwnerID, stored.WorkflowID).
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_id = ? AND workflow_id = ?", target.ID, target.OwnerID, target.WorkflowID).
			First(&stored).Error; err != nil {
			return err
		}
		if stored.Status != workflow.RuleSetStatusPublished && stored.Status != workflow.RuleSetStatusSuperseded {
			return fmt.Errorf("%w: rollback target status is %s", workflow.ErrRuleSetConflict, stored.Status)
		}
		var latest int
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ?", stored.OwnerID, stored.WorkflowID).
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
		clone.Edges = nil
		clone.CompiledSnapshotJSON = nil
		clone.CompiledHash = ""
		if err := tx.Create(clone).Error; err != nil {
			return err
		}
		if compile == nil {
			return fmt.Errorf("rollback compiler is not configured")
		}
		nodes, edges, snapshot, compiledHash, tokenEstimator, err := compile(clone.ID, clone.VersionNo)
		if err != nil {
			return err
		}
		clone.CompiledSnapshotJSON = append([]byte(nil), snapshot...)
		clone.CompiledHash = compiledHash
		clone.TokenEstimatorVersion = tokenEstimator
		if err := tx.Save(clone).Error; err != nil {
			return err
		}
		if err := replaceRuleSetChildren(tx, clone.ID, nodes, edges); err != nil {
			return err
		}
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ? AND status = ? AND id <> ?", stored.OwnerID, stored.WorkflowID, workflow.RuleSetStatusPublished, clone.ID).
			Update("status", workflow.RuleSetStatusSuperseded).Error; err != nil {
			return err
		}
		updated := tx.Model(&workflow.Profile{}).
			Where("owner_id = ? AND workflow_id = ? AND deleted_at IS NULL", stored.OwnerID, stored.WorkflowID).
			Update("active_rule_set_id", clone.ID)
		if updated.Error != nil || updated.RowsAffected != 1 {
			if updated.Error != nil {
				return updated.Error
			}
			return fmt.Errorf("workflow profile not found while rolling back rule set")
		}
		return nil
	})
}

func replaceRuleSetChildren(tx *gorm.DB, ruleSetID int64, nodes []workflow.RuleNode, edges []workflow.RuleEdge) error {
	if err := tx.Where("rule_set_id = ?", ruleSetID).Delete(&workflow.RuleEdge{}).Error; err != nil {
		return err
	}
	if err := tx.Where("rule_set_id = ?", ruleSetID).Delete(&workflow.RuleNode{}).Error; err != nil {
		return err
	}
	for index := range nodes {
		nodes[index].ID = 0
		nodes[index].RuleSetID = ruleSetID
	}
	if len(nodes) > 0 {
		if err := tx.Create(&nodes).Error; err != nil {
			return err
		}
	}
	for index := range edges {
		edges[index].ID = 0
		edges[index].RuleSetID = ruleSetID
	}
	if len(edges) > 0 {
		if err := tx.Create(&edges).Error; err != nil {
			return err
		}
	}
	return nil
}

func loadRuleSetChildren(db *gorm.DB, item *workflow.RuleSet) error {
	if err := db.Where("rule_set_id = ?", item.ID).Order("topological_order ASC, rule_id ASC").Find(&item.Nodes).Error; err != nil {
		return err
	}
	return db.Where("rule_set_id = ?", item.ID).Order("id ASC").Find(&item.Edges).Error
}
