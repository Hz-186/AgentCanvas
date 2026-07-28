package mysql

import (
	"context"
	"fmt"
	"math"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemoryRepository struct{ db *gorm.DB }

func NewMemoryRepository(db *gorm.DB) *MemoryRepository { return &MemoryRepository{db: db} }

func (r *MemoryRepository) Create(ctx context.Context, item *memory.Memory) error {
	now := time.Now().UTC()
	item.ApplyV2Defaults()
	if item.MemoryLevel == "" {
		item.MemoryLevel = memory.LevelLongTerm
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var create *gorm.DB
		if item.SourceKey != nil {
			create = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "source_key"}}, DoNothing: true}).Create(item)
		} else {
			create = tx.Create(item)
		}
		if err := create.Error; err != nil {
			return err
		}
		if create.RowsAffected == 0 && item.SourceKey != nil {
			if err := tx.Where("owner_id = ? AND source_key = ?", item.OwnerID, *item.SourceKey).First(item).Error; err != nil {
				return err
			}
			return nil
		}
		return enqueueMemoryContextResource(ctx, tx, item, now)
	})
}

func (r *MemoryRepository) Update(ctx context.Context, item *memory.Memory) error {
	item.ApplyV2Defaults()
	if item.MemoryLevel == "" {
		item.MemoryLevel = memory.LevelLongTerm
	}
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(item).Error; err != nil {
			return err
		}
		return enqueueMemoryContextResource(ctx, tx, item, item.UpdatedAt)
	})
}

func (r *MemoryRepository) Replace(ctx context.Context, ownerID, supersededID int64, replacement *memory.Memory) error {
	if replacement == nil || ownerID <= 0 || supersededID <= 0 || replacement.OwnerID != ownerID {
		return fmt.Errorf("invalid memory replacement")
	}
	now := time.Now().UTC()
	replacement.ApplyV2Defaults()
	replacement.MemoryLevel = memory.LevelLongTerm
	replacement.Status = memory.StatusActive
	replacement.SupersedesID = &supersededID
	if replacement.CreatedAt.IsZero() {
		replacement.CreatedAt = now
	}
	replacement.UpdatedAt = now
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var previous memory.Memory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, supersededID).First(&previous).Error; err != nil {
			return err
		}
		if previous.Status != "" && previous.Status != memory.StatusActive && previous.Status != memory.StatusSuperseded {
			return fmt.Errorf("memory %d cannot be superseded from status %s", supersededID, previous.Status)
		}

		created := true
		var create *gorm.DB
		if replacement.SourceKey != nil {
			create = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "source_key"}}, DoNothing: true}).Create(replacement)
		} else {
			create = tx.Create(replacement)
		}
		if create.Error != nil {
			return create.Error
		}
		if create.RowsAffected == 0 && replacement.SourceKey != nil {
			created = false
			if err := tx.Where("owner_id = ? AND source_key = ?", ownerID, *replacement.SourceKey).First(replacement).Error; err != nil {
				return err
			}
			if replacement.SupersedesID == nil || *replacement.SupersedesID != supersededID {
				return fmt.Errorf("idempotency key belongs to a different memory replacement")
			}
		}

		if previous.Status == memory.StatusSuperseded && !created {
			return nil
		}
		if previous.Status != memory.StatusActive {
			return fmt.Errorf("memory %d is already superseded", supersededID)
		}
		if created {
			if err := enqueueMemoryContextResource(ctx, tx, replacement, now); err != nil {
				return err
			}
		}
		if err := tx.Model(&memory.Memory{}).Where("owner_id = ? AND id = ? AND status = ? AND deleted_at IS NULL", ownerID, supersededID, memory.StatusActive).
			Updates(map[string]any{"status": memory.StatusSuperseded, "updated_at": now}).Error; err != nil {
			return err
		}
		return enqueueContextResource(ctx, tx, ownerID, 0, contextresource.TypeLongTermMemory, supersededID, contextresource.OperationDelete, "")
	})
}

func (r *MemoryRepository) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	var item memory.Memory
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *MemoryRepository) FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	items := make([]memory.Memory, 0, len(ids))
	if len(ids) == 0 {
		return items, nil
	}
	err := r.db.WithContext(ctx).Where("owner_id = ? AND id IN ? AND deleted_at IS NULL", ownerID, ids).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	query = filterMemories(query, memoryTypes, conversationID)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) ListFiltered(ctx context.Context, ownerID int64, filter memory.ListFilter) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	query = filterMemories(query, filter.MemoryTypes, filter.ConversationID)
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	if len(filter.ScopeTypes) > 0 {
		query = query.Where("scope_type IN ?", filter.ScopeTypes)
	}
	if filter.ScopeID != nil {
		query = query.Where("scope_id = ?", *filter.ScopeID)
	}
	if len(filter.Sources) > 0 {
		query = query.Where("source IN ?", filter.Sources)
	}
	limit, offset := filter.Limit, filter.Offset
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	var items []memory.Memory
	query := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Where("status = ? AND conflict_flag = ?", memory.StatusActive, false).
		Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC())
	query = filterMemories(query, memoryTypes, conversationID)
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	err := query.Order("importance DESC, updated_at DESC, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&memory.Memory{}).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
			Updates(map[string]any{"deleted_at": now, "updated_at": now})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return enqueueContextResource(ctx, tx, ownerID, 0, contextresource.TypeLongTermMemory, id, contextresource.OperationDelete, "")
	})
}

func (r *MemoryRepository) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id IN ? AND deleted_at IS NULL AND status = ?", ownerID, ids, memory.StatusActive).
		Updates(map[string]any{"last_used_at": now, "updated_at": now, "access_count": gorm.Expr("access_count + 1")}).Error
}

func (r *MemoryRepository) ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]memory.Memory, error) {
	var items []memory.Memory
	if ownerID <= 0 {
		return items, nil
	}
	query := r.db.WithContext(ctx).Where("memory_level = ? AND deleted_at IS NULL", level).
		Where("owner_id = ?", ownerID).
		Where("status = ? AND conflict_flag = ?", memory.StatusActive, false).
		Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC())
	if len(memoryTypes) > 0 {
		query = query.Where("memory_type IN ?", memoryTypes)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	err := query.Order("importance DESC, access_count DESC, updated_at DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *MemoryRepository) ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	var ownerIDs []int64
	err := r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("deleted_at IS NULL").
		Where("status = ?", memory.StatusActive).
		Where("memory_level IN ?", []string{memory.LevelShortTerm, memory.LevelLongTerm}).
		Distinct("owner_id").
		Order("owner_id ASC").
		Limit(limit).
		Pluck("owner_id", &ownerIDs).Error
	return ownerIDs, err
}

func (r *MemoryRepository) IncrementAccessCount(ctx context.Context, ownerID int64, id int64) error {
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		UpdateColumn("access_count", gorm.Expr("access_count + 1")).Error
}

func (r *MemoryRepository) IncrementConsolidationCount(ctx context.Context, ownerID int64, id int64) error {
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		UpdateColumn("consolidation_count", gorm.Expr("consolidation_count + 1")).Error
}

func (r *MemoryRepository) MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	now := time.Now().UTC()
	var count int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&memory.Memory{}).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
			Where("expires_at IS NOT NULL AND expires_at <= ?", now)
		if maxAgeDays > 0 {
			cutoff := now.AddDate(0, 0, -maxAgeDays)
			query = tx.Model(&memory.Memory{}).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
				Where("(expires_at IS NOT NULL AND expires_at <= ?) OR (memory_level = ? AND updated_at <= ?)", now, memory.LevelShortTerm, cutoff)
		}
		var ids []int64
		if err := query.Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
			return err
		}
		result := tx.Model(&memory.Memory{}).Where("owner_id = ? AND id IN ? AND deleted_at IS NULL", ownerID, ids).
			Updates(map[string]any{"deleted_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		count = result.RowsAffected
		for _, id := range ids {
			if err := enqueueContextResource(ctx, tx, ownerID, 0, contextresource.TypeLongTermMemory, id, contextresource.OperationDelete, ""); err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}

func (r *MemoryRepository) UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-23 * time.Hour)
	decayBase := "GREATEST(COALESCE(last_decay_at, created_at), COALESCE(last_used_at, created_at))"
	result := r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND deleted_at IS NULL AND status = ? AND memory_level = ?", ownerID, memory.StatusActive, memory.LevelLongTerm).
		Where(decayBase+" <= ?", cutoff).
		Updates(map[string]any{
			"importance":    gorm.Expr("GREATEST(?, importance * EXP(? * (TIMESTAMPDIFF(SECOND, "+decayBase+", ?) / 86400.0)))", decayMinImportance, -decayRate, now),
			"last_decay_at": now,
			"updated_at":    now,
		})
	var count int64
	if result.Error != nil {
		return 0, result.Error
	}
	count = result.RowsAffected
	return count, nil
}

func (r *MemoryRepository) SetEmbedding(ctx context.Context, ownerID, id int64, embedding []byte) error {
	return r.db.WithContext(ctx).Model(&memory.Memory{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).
		Update("embedding", embedding).Error
}

const decayMinImportance = 0.05

func initialDecayedImportance(originalImportance float64, daysSinceLastAccess int, decayRate float64) float64 {
	decayed := originalImportance * math.Exp(-decayRate*float64(daysSinceLastAccess))
	if decayed < decayMinImportance {
		return decayMinImportance
	}
	return decayed
}

func filterMemories(query *gorm.DB, memoryTypes []string, conversationID *int64) *gorm.DB {
	if len(memoryTypes) > 0 {
		query = query.Where("memory_type IN ?", memoryTypes)
	}
	if conversationID != nil {
		query = query.Where("(conversation_id IS NULL OR conversation_id = ?)", *conversationID)
	}
	return query
}

func enqueueMemoryContextResource(ctx context.Context, tx *gorm.DB, item *memory.Memory, now time.Time) error {
	if item != nil && item.IsRecallable(now) {
		return enqueueContextResource(ctx, tx, item.OwnerID, 0, contextresource.TypeLongTermMemory, item.ID, contextresource.OperationUpsert, memoryContextText(*item))
	}
	if item == nil {
		return nil
	}
	return enqueueContextResource(ctx, tx, item.OwnerID, 0, contextresource.TypeLongTermMemory, item.ID, contextresource.OperationDelete, "")
}

type MemoryWriteLogRepository struct{ db *gorm.DB }

func NewMemoryWriteLogRepository(db *gorm.DB) *MemoryWriteLogRepository {
	return &MemoryWriteLogRepository{db: db}
}

func (r *MemoryWriteLogRepository) Create(ctx context.Context, item *memory.WriteLog) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *MemoryWriteLogRepository) ListByRun(ctx context.Context, ownerID, runID int64) ([]memory.WriteLog, error) {
	var items []memory.WriteLog
	err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).Order("id ASC").Find(&items).Error
	return items, err
}

type MemoryRecallLogRepository struct{ db *gorm.DB }

func NewMemoryRecallLogRepository(db *gorm.DB) *MemoryRecallLogRepository {
	return &MemoryRecallLogRepository{db: db}
}

func (r *MemoryRecallLogRepository) Create(ctx context.Context, item *memory.RecallLog) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *MemoryRecallLogRepository) List(ctx context.Context, ownerID, memoryID int64, limit int) ([]memory.RecallLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if memoryID > 0 {
		query = query.Where("JSON_CONTAINS(injected_json, JSON_OBJECT('memory_id', ?))", memoryID)
	}
	var items []memory.RecallLog
	return items, query.Order("id DESC").Limit(limit).Find(&items).Error
}

func (r *MemoryRecallLogRepository) SetFeedback(ctx context.Context, ownerID, id int64, feedback string) error {
	return r.db.WithContext(ctx).Model(&memory.RecallLog{}).Where("owner_id = ? AND id = ?", ownerID, id).Update("feedback", feedback).Error
}
