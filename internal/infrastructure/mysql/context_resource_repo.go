package mysql

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultContextOutboxMaxAttempts = 8

type ContextResourceRepository struct{ db *gorm.DB }

type ContextBackfillResult struct {
	ResourceType string `json:"resource_type"`
	Scanned      int    `json:"scanned"`
	Enqueued     int    `json:"enqueued"`
	NextID       int64  `json:"next_id"`
	Done         bool   `json:"done"`
}

func NewContextResourceRepository(db *gorm.DB) *ContextResourceRepository {
	return &ContextResourceRepository{db: db}
}

func (r *ContextResourceRepository) Backfill(ctx context.Context, resourceType string, afterID int64, limit int, dryRun bool) (ContextBackfillResult, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	result := ContextBackfillResult{ResourceType: resourceType, NextID: afterID}
	type candidate struct {
		ID             int64
		OwnerID        int64
		AgentID        int64
		ConversationID int64
		Content        string
	}
	candidates := make([]candidate, 0, limit)
	switch resourceType {
	case contextresource.TypeLongTermMemory:
		var items []memory.Memory
		if err := r.db.WithContext(ctx).Where("id > ? AND deleted_at IS NULL AND status = ? AND has_conflict = ? AND (expires_at IS NULL OR expires_at > ?) AND retention_tier IN ?", afterID, memory.StatusActive, false, time.Now().UTC(), []string{memory.TierShortTerm, memory.TierLongTerm}).Order("id ASC").Limit(limit).Find(&items).Error; err != nil {
			return result, err
		}
		for i := range items {
			agentID, conversationID, _ := memoryIndexScope(&items[i])
			candidates = append(candidates, candidate{ID: items[i].ID, OwnerID: items[i].OwnerID, AgentID: agentID, ConversationID: conversationID, Content: memoryContextText(items[i])})
		}
	case contextresource.TypeSkill:
		var items []skill.Skill
		if err := r.db.WithContext(ctx).Where("id > ? AND deleted_at IS NULL", afterID).Order("id ASC").Limit(limit).Find(&items).Error; err != nil {
			return result, err
		}
		for i := range items {
			candidates = append(candidates, candidate{ID: items[i].ID, OwnerID: items[i].OwnerID, Content: skillContextText(items[i])})
		}
	case contextresource.TypeTool:
		var items []tool.Definition
		if err := r.db.WithContext(ctx).Where("id > ? AND deleted_at IS NULL", afterID).Order("id ASC").Limit(limit).Find(&items).Error; err != nil {
			return result, err
		}
		for i := range items {
			candidates = append(candidates, candidate{ID: items[i].ID, OwnerID: items[i].OwnerID, Content: toolContextText(items[i])})
		}
	case contextresource.TypeConversationMessage:
		var items []conversation.Message
		if err := r.db.WithContext(ctx).Where("id > ? AND archived_at IS NULL", afterID).Order("id ASC").Limit(limit).Find(&items).Error; err != nil {
			return result, err
		}
		for i := range items {
			agentID := int64(0)
			if err := r.db.WithContext(ctx).Raw("SELECT COALESCE(agent_id, 0) FROM conversations WHERE owner_id = ? AND id = ?", items[i].OwnerID, items[i].ConversationID).Scan(&agentID).Error; err != nil {
				return result, err
			}
			candidates = append(candidates, candidate{ID: items[i].ID, OwnerID: items[i].OwnerID, AgentID: agentID, ConversationID: items[i].ConversationID, Content: messageContextText(items[i])})
		}
	default:
		return result, fmt.Errorf("unsupported context resource type %q", resourceType)
	}
	result.Scanned = len(candidates)
	if len(candidates) == 0 {
		result.Done = true
		return result, nil
	}
	result.NextID = candidates[len(candidates)-1].ID
	result.Done = len(candidates) < limit
	if dryRun {
		return result, nil
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range candidates {
			if err := enqueueContextResource(ctx, tx, candidates[i].OwnerID, candidates[i].AgentID, candidates[i].ConversationID, resourceType, candidates[i].ID, contextresource.OperationUpsert, candidates[i].Content); err != nil {
				return err
			}
			result.Enqueued++
		}
		return nil
	})
	return result, err
}

func enqueueContextResource(ctx context.Context, tx *gorm.DB, ownerID, agentID, conversationID int64, resourceType string, resourceID int64, operation, content string) error {
	if tx == nil || ownerID <= 0 || resourceID <= 0 || strings.TrimSpace(resourceType) == "" {
		return nil
	}
	profile := contextresource.EmbeddingProfileFromContext(ctx)
	contentHash := contextresource.HashContent(content)
	if operation == contextresource.OperationDelete && profile.Hash == "" {
		var prior contextresource.OutboxItem
		err := tx.Where("owner_id = ? AND resource_type = ? AND resource_id = ? AND embedding_profile_hash <> ''", ownerID, resourceType, strconv.FormatInt(resourceID, 10)).
			Order("id DESC").First(&prior).Error
		if err == nil {
			profile = contextresource.EmbeddingProfile{ProviderID: prior.EmbeddingProviderID, Model: prior.EmbeddingModel, Dimensions: prior.EmbeddingDimensions, Hash: prior.EmbeddingProfileHash}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	now := time.Now().UTC()
	item := contextresource.OutboxItem{
		BaseModel: domain.BaseModel{OwnerID: ownerID, CreatedAt: now, UpdatedAt: now}, AgentID: agentID, ConversationID: conversationID, ResourceType: resourceType, ResourceID: strconv.FormatInt(resourceID, 10),
		Operation: operation, ContentHash: contentHash, EmbeddingProviderID: profile.ProviderID, EmbeddingModel: profile.Model,
		EmbeddingDimensions: profile.Dimensions, EmbeddingProfileHash: profile.Hash, Status: contextresource.StatusPending,
		MaxAttempts: defaultContextOutboxMaxAttempts, AvailableAt: now,
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&item).Error
}

func (r *ContextResourceRepository) Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]contextresource.OutboxItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if lease <= 0 {
		lease = time.Minute
	}
	var items []contextresource.OutboxItem
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? OR (status = ? AND lease_expires_at < ?)) AND available_at <= ?", contextresource.StatusPending, contextresource.StatusProcessing, now, now).
			Order("id ASC").Limit(limit).Find(&items).Error; err != nil {
			return err
		}
		leaseUntil := now.Add(lease)
		for i := range items {
			result := tx.Model(&contextresource.OutboxItem{}).Where("id = ?", items[i].ID).Updates(map[string]any{
				"status": contextresource.StatusProcessing, "locked_by": workerID, "locked_at": now, "lease_expires_at": leaseUntil, "updated_at": now,
			})
			if result.Error != nil {
				return result.Error
			}
			items[i].Status, items[i].LockedBy, items[i].LockedAt, items[i].LeaseExpiresAt = contextresource.StatusProcessing, workerID, &now, &leaseUntil
		}
		return nil
	})
	return items, err
}

func (r *ContextResourceRepository) Complete(ctx context.Context, id int64, workerID string, profile contextresource.EmbeddingProfile) error {
	profile = profile.Normalized()
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&contextresource.OutboxItem{}).
		Where("id = ? AND status = ? AND locked_by = ? AND lease_expires_at > ?", id, contextresource.StatusProcessing, workerID, now).
		Updates(map[string]any{"status": contextresource.StatusCompleted, "embedding_provider_id": profile.ProviderID, "embedding_model": profile.Model,
			"embedding_dimensions": profile.Dimensions, "embedding_profile_hash": profile.Hash, "completed_at": now,
			"locked_by": "", "locked_at": nil, "lease_expires_at": nil, "last_error": "", "updated_at": now})
	return contextOutboxLeaseResult(result)
}

func (r *ContextResourceRepository) Renew(ctx context.Context, id int64, workerID string, lease time.Duration) error {
	if lease <= 0 {
		lease = time.Minute
	}
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&contextresource.OutboxItem{}).
		Where("id = ? AND status = ? AND locked_by = ?", id, contextresource.StatusProcessing, workerID).
		Updates(map[string]any{"lease_expires_at": now.Add(lease), "updated_at": now})
	return contextOutboxLeaseResult(result)
}

func (r *ContextResourceRepository) Retry(ctx context.Context, id int64, workerID string, cause error, next time.Time) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item contextresource.OutboxItem
		now := time.Now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ? AND locked_by = ? AND lease_expires_at > ?", id, contextresource.StatusProcessing, workerID, now).First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return contextresource.ErrLeaseLost
			}
			return err
		}
		attemptCount := item.AttemptCount + 1
		status := contextresource.StatusPending
		if item.MaxAttempts <= 0 || attemptCount >= item.MaxAttempts {
			status = contextresource.StatusDeadLetter
		}
		return tx.Model(&contextresource.OutboxItem{}).Where("id = ?", id).Updates(map[string]any{
			"status": status, "attempt_count": attemptCount, "available_at": next.UTC(), "last_error": message,
			"locked_by": "", "locked_at": nil, "lease_expires_at": nil, "updated_at": now,
		}).Error
	})
}

func contextOutboxLeaseResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return contextresource.ErrLeaseLost
	}
	return nil
}

func (r *ContextResourceRepository) LoadDocument(ctx context.Context, item contextresource.OutboxItem) (*contextresource.Document, error) {
	id, err := strconv.ParseInt(item.ResourceID, 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("invalid %s resource id %q", item.ResourceType, item.ResourceID)
	}
	switch item.ResourceType {
	case contextresource.TypeLongTermMemory:
		var value memory.Memory
		if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", item.OwnerID, id).First(&value).Error; err != nil {
			return nilOrLoadError(err)
		}
		agentID, conversationID, projectID := memoryIndexScope(&value)
		if value.ScopeType == memory.ScopeProject {
			projectID = value.ScopeID
			conversationID = 0
		}
		if !value.IsRecallable(time.Now().UTC()) {
			return nilOrLoadError(gorm.ErrRecordNotFound)
		}
		item.AgentID = agentID
		return document(item, memoryContextText(value), conversationID, projectID, map[string]any{"memory_type": value.MemoryType, "retention_tier": value.RetentionTier, "scope_type": value.ScopeType, "scope_id": value.ScopeID, "status": value.Status, "has_conflict": value.HasConflict, "importance": value.Importance}), nil
	case contextresource.TypeSkill:
		var value skill.Skill
		if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", item.OwnerID, id).First(&value).Error; err != nil {
			return nilOrLoadError(err)
		}
		return document(item, skillContextText(value), 0, 0, map[string]any{"enabled": value.Enabled, "skill_type": value.SkillType}), nil
	case contextresource.TypeTool:
		var value tool.Definition
		if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", item.OwnerID, id).First(&value).Error; err != nil {
			return nilOrLoadError(err)
		}
		return document(item, toolContextText(value), 0, 0, map[string]any{"enabled": value.Enabled, "tool_type": value.ToolType}), nil
	case contextresource.TypeConversationMessage:
		var value conversation.Message
		if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND archived_at IS NULL", item.OwnerID, id).First(&value).Error; err != nil {
			return nilOrLoadError(err)
		}
		return document(item, messageContextText(value), value.ConversationID, 0, map[string]any{"role": value.Role, "created_at": value.CreatedAt.Unix()}), nil
	default:
		return nil, fmt.Errorf("unsupported context resource type %q", item.ResourceType)
	}
}

func nilOrLoadError(err error) (*contextresource.Document, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

func document(item contextresource.OutboxItem, content string, conversationID, projectID int64, metadata map[string]any) *contextresource.Document {
	return &contextresource.Document{OwnerID: item.OwnerID, AgentID: item.AgentID, ProjectID: projectID, ResourceType: item.ResourceType,
		ResourceID: item.ResourceID, Content: content, ContentHash: contextresource.HashContent(content), ConversationID: conversationID, Metadata: metadata}
}

func memoryContextText(item memory.Memory) string {
	return strings.Join([]string{item.Title, item.Content, item.MemoryType, item.Source}, "\n")
}

func skillContextText(item skill.Skill) string {
	return strings.Join([]string{item.Name, item.Description, string(item.TagsJSON), item.ContentMarkdown}, "\n")
}

func toolContextText(item tool.Definition) string {
	return strings.Join([]string{item.Name, item.Description, string(item.InputSchemaJSON), string(item.OutputSchemaJSON)}, "\n")
}

func messageContextText(item conversation.Message) string {
	return strings.Join([]string{item.Role, item.Content}, "\n")
}

var _ contextresource.Repository = (*ContextResourceRepository)(nil)
