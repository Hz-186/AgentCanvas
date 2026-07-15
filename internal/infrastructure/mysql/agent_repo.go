package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentRepository struct{ db *gorm.DB }

func NewAgentRepository(db *gorm.DB) *AgentRepository { return &AgentRepository{db: db} }

func (r *AgentRepository) Create(ctx context.Context, item *agentdomain.Agent) error {
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if item.Status == "" {
		item.Status = agentdomain.StatusDraft
	}
	if err := encodeAgentDefinition(item); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentRepository) ListByOwner(ctx context.Context, ownerID int64) ([]agentdomain.Agent, error) {
	var items []agentdomain.Agent
	err := r.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("updated_at DESC, id DESC").Find(&items).Error
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := decodeAgentDefinition(&items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *AgentRepository) FindByID(ctx context.Context, ownerID, id int64) (*agentdomain.Agent, error) {
	var item agentdomain.Agent
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, id).First(&item).Error; err != nil {
		return nil, err
	}
	if err := decodeAgentDefinition(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentRepository) Update(ctx context.Context, item *agentdomain.Agent) error {
	item.UpdatedAt = time.Now().UTC()
	if err := encodeAgentDefinition(item); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&agentdomain.Agent{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", item.ID, item.OwnerID).
		Updates(map[string]any{
			"name": item.Name, "description": item.Description, "avatar_url": item.AvatarURL,
			"status": item.Status, "draft_definition_json": item.DraftDefinitionJSON,
			"current_release_id": item.CurrentReleaseID, "updated_at": item.UpdatedAt,
		}).Error
}

func (r *AgentRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&agentdomain.Agent{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now, "status": agentdomain.StatusArchived}).Error
}

func (r *AgentRepository) CreateRelease(ctx context.Context, item *agentdomain.Release) error {
	item.CreatedAt = time.Now().UTC()
	if err := encodeReleaseDefinition(item); err != nil {
		return err
	}
	if len(item.ResourceVersions) == 0 {
		item.ResourceVersions = json.RawMessage(`{}`)
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentRepository) ListReleases(ctx context.Context, ownerID, agentID int64) ([]agentdomain.Release, error) {
	var items []agentdomain.Release
	err := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ?", ownerID, agentID).
		Order("version_no DESC").Find(&items).Error
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := decodeReleaseDefinition(&items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *AgentRepository) FindReleaseByID(ctx context.Context, ownerID, id int64) (*agentdomain.Release, error) {
	var item agentdomain.Release
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error; err != nil {
		return nil, err
	}
	if err := decodeReleaseDefinition(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentRepository) NextReleaseVersion(ctx context.Context, ownerID, agentID int64) (int, error) {
	var maxVersion int
	err := r.db.WithContext(ctx).Model(&agentdomain.Release{}).
		Where("owner_id = ? AND agent_id = ?", ownerID, agentID).
		Select("COALESCE(MAX(version_no), 0)").Scan(&maxVersion).Error
	return maxVersion + 1, err
}

func (r *AgentRepository) SetCurrentRelease(ctx context.Context, ownerID, agentID, releaseID int64) error {
	return r.db.WithContext(ctx).Model(&agentdomain.Agent{}).
		Where("owner_id = ? AND id = ? AND deleted_at IS NULL", ownerID, agentID).
		Updates(map[string]any{"current_release_id": releaseID, "status": agentdomain.StatusActive, "updated_at": time.Now().UTC()}).Error
}

func encodeAgentDefinition(item *agentdomain.Agent) error {
	definition := item.DraftDefinition.Normalize()
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	item.DraftDefinition, item.DraftDefinitionJSON = definition, raw
	return nil
}

func decodeAgentDefinition(item *agentdomain.Agent) error {
	if len(item.DraftDefinitionJSON) == 0 {
		item.DraftDefinition = agentdomain.Definition{}.Normalize()
		return nil
	}
	return json.Unmarshal(item.DraftDefinitionJSON, &item.DraftDefinition)
}

func encodeReleaseDefinition(item *agentdomain.Release) error {
	raw, checksum, err := item.Definition.Snapshot()
	if err != nil {
		return err
	}
	item.DefinitionJSON, item.Checksum = raw, checksum
	return nil
}

func decodeReleaseDefinition(item *agentdomain.Release) error {
	if len(item.DefinitionJSON) == 0 {
		return errors.New("agent release definition is missing")
	}
	return json.Unmarshal(item.DefinitionJSON, &item.Definition)
}

type AgentTurnRepository struct{ db *gorm.DB }

func NewAgentTurnRepository(db *gorm.DB) *AgentTurnRepository { return &AgentTurnRepository{db: db} }

func (r *AgentTurnRepository) Create(ctx context.Context, item *agentdomain.Turn) error {
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if item.Status == "" {
		item.Status = agentdomain.TurnStatusQueued
	}
	if item.MaxAttempts <= 0 {
		item.MaxAttempts = 3
	}
	if len(item.InputJSON) == 0 {
		item.InputJSON = json.RawMessage(`{}`)
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *AgentTurnRepository) CreateWithArtifacts(ctx context.Context, item *agentdomain.Turn, userMessage *conversation.Message, run *workflow.Run) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		userMessage.CreatedAt = now
		normalizeMessage(userMessage)
		if err := tx.Create(userMessage).Error; err != nil {
			return err
		}
		if err := syncConversationMessageJSON(ctx, tx, userMessage.OwnerID, userMessage.ConversationID, now); err != nil {
			return err
		}
		run.CreatedAt, run.UpdatedAt = now, now
		if run.StartedAt.IsZero() {
			run.StartedAt = now
		}
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		item.RunID, item.UserMessageID = &run.ID, userMessage.ID
		item.CreatedAt, item.UpdatedAt = now, now
		if item.Status == "" {
			item.Status = agentdomain.TurnStatusQueued
		}
		if item.MaxAttempts <= 0 {
			item.MaxAttempts = 3
		}
		if len(item.InputJSON) == 0 {
			item.InputJSON = json.RawMessage(`{}`)
		}
		return tx.Create(item).Error
	})
}

func (r *AgentTurnRepository) CompleteWithMessage(ctx context.Context, item *agentdomain.Turn, assistantMessage *conversation.Message, run *workflow.Run) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owned agentdomain.Turn
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", item.ID, item.OwnerID)
		if item.LeaseToken != "" {
			query = query.Where("lease_token = ?", item.LeaseToken)
		}
		if err := query.First(&owned).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		assistantMessage.CreatedAt = now
		normalizeMessage(assistantMessage)
		if err := tx.Create(assistantMessage).Error; err != nil {
			return err
		}
		if err := syncConversationMessageJSON(ctx, tx, assistantMessage.OwnerID, assistantMessage.ConversationID, now); err != nil {
			return err
		}
		item.AssistantMessageID, item.UpdatedAt = &assistantMessage.ID, now
		if err := tx.Save(item).Error; err != nil {
			return err
		}
		run.UpdatedAt = now
		return tx.Save(run).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) && item.LeaseToken != "" {
		return agentdomain.ErrLeaseLost
	}
	return err
}

func (r *AgentTurnRepository) FindByID(ctx context.Context, ownerID, id int64) (*agentdomain.Turn, error) {
	var item agentdomain.Turn
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentTurnRepository) FindByIdempotencyKey(ctx context.Context, ownerID, conversationID int64, key string) (*agentdomain.Turn, error) {
	var item agentdomain.Turn
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND conversation_id = ? AND idempotency_key = ?", ownerID, conversationID, key).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentTurnRepository) FindByRunID(ctx context.Context, ownerID, runID int64) (*agentdomain.Turn, error) {
	var item agentdomain.Turn
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND run_id = ?", ownerID, runID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentTurnRepository) Update(ctx context.Context, item *agentdomain.Turn) error {
	item.UpdatedAt = time.Now().UTC()
	query := r.db.WithContext(ctx).Model(&agentdomain.Turn{}).Where("id = ? AND owner_id = ?", item.ID, item.OwnerID)
	if item.LeaseToken != "" {
		query = query.Where("lease_token = ?", item.LeaseToken)
	}
	result := query.Select("*").Updates(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 && item.LeaseToken != "" {
		return agentdomain.ErrLeaseLost
	}
	return nil
}

func (r *AgentTurnRepository) ListQueued(ctx context.Context, limit int) ([]agentdomain.Turn, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var items []agentdomain.Turn
	err := r.db.WithContext(ctx).Where("status = ?", agentdomain.TurnStatusQueued).Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *AgentTurnRepository) ClaimNext(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*agentdomain.Turn, error) {
	var claimed agentdomain.Turn
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status IN ? AND (retry_at IS NULL OR retry_at <= ?) AND attempt_count < max_attempts", []string{agentdomain.TurnStatusQueued, agentdomain.TurnStatusRetryWait}, now).
			Order("id ASC").First(&claimed).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": agentdomain.TurnStatusRunning, "worker_id": workerID, "lease_token": leaseToken,
			"lease_expires_at": leaseUntil, "last_heartbeat_at": now, "retry_at": nil,
			"attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": now}
		if claimed.StartedAt == nil {
			updates["started_at"] = now
		}
		if err := tx.Model(&agentdomain.Turn{}).Where("id = ?", claimed.ID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", claimed.ID).First(&claimed).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, agentdomain.ErrNoTurnAvailable
	}
	return &claimed, err
}

func (r *AgentTurnRepository) RenewLease(ctx context.Context, turnID int64, leaseToken string, leaseUntil time.Time) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&agentdomain.Turn{}).
		Where("id = ? AND lease_token = ? AND status = ?", turnID, leaseToken, agentdomain.TurnStatusRunning).
		Updates(map[string]any{"lease_expires_at": leaseUntil, "last_heartbeat_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AgentTurnRepository) ListExpiredRunning(ctx context.Context, before time.Time, limit int) ([]agentdomain.Turn, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var items []agentdomain.Turn
	err := r.db.WithContext(ctx).Where("status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?", agentdomain.TurnStatusRunning, before).
		Order("lease_expires_at ASC, id ASC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *AgentTurnRepository) RequeueExpired(ctx context.Context, turnID int64, retryAt time.Time, reason string) error {
	return r.db.WithContext(ctx).Model(&agentdomain.Turn{}).
		Where("id = ? AND status = ? AND lease_expires_at < ?", turnID, agentdomain.TurnStatusRunning, time.Now().UTC()).
		Updates(map[string]any{"status": agentdomain.TurnStatusRetryWait, "worker_id": "", "lease_token": "", "lease_expires_at": nil,
			"retry_at": retryAt, "error_message": reason, "updated_at": time.Now().UTC()}).Error
}

func (r *AgentTurnRepository) PauseExpired(ctx context.Context, turnID int64, reason string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&agentdomain.Turn{}).
		Where("id = ? AND status = ? AND lease_expires_at < ?", turnID, agentdomain.TurnStatusRunning, now).
		Updates(map[string]any{"status": agentdomain.TurnStatusPaused, "worker_id": "", "lease_token": "", "lease_expires_at": nil,
			"error_message": reason, "finished_at": now, "updated_at": now}).Error
}

type AgentImprovementRepository struct{ db *gorm.DB }

func NewAgentImprovementRepository(db *gorm.DB) *AgentImprovementRepository {
	return &AgentImprovementRepository{db: db}
}

func (r *AgentImprovementRepository) EnqueueReview(ctx context.Context, item *agentdomain.ImprovementReview) error {
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if item.Status == "" {
		item.Status = agentdomain.ReviewStatusPending
	}
	if item.MaxAttempts <= 0 {
		item.MaxAttempts = 3
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

func (r *AgentImprovementRepository) ClaimNextReview(ctx context.Context, workerID, leaseToken string, leaseUntil time.Time) (*agentdomain.ImprovementReview, error) {
	var claimed agentdomain.ImprovementReview
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND (retry_at IS NULL OR retry_at <= ?) AND attempt_count < max_attempts", agentdomain.ReviewStatusPending, now).
			Order("id ASC").First(&claimed).Error; err != nil {
			return err
		}
		if err := tx.Model(&agentdomain.ImprovementReview{}).Where("id = ?", claimed.ID).Updates(map[string]any{
			"status": agentdomain.ReviewStatusRunning, "worker_id": workerID, "lease_token": leaseToken,
			"lease_expires_at": leaseUntil.UTC(), "last_heartbeat_at": now, "retry_at": nil,
			"attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", claimed.ID).First(&claimed).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, agentdomain.ErrNoReviewAvailable
	}
	return &claimed, err
}

func (r *AgentImprovementRepository) RenewReviewLease(ctx context.Context, reviewID int64, leaseToken string, leaseUntil time.Time) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&agentdomain.ImprovementReview{}).
		Where("id = ? AND lease_token = ? AND status = ?", reviewID, leaseToken, agentdomain.ReviewStatusRunning).
		Updates(map[string]any{"lease_expires_at": leaseUntil.UTC(), "last_heartbeat_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentdomain.ErrLeaseLost
	}
	return nil
}

func (r *AgentImprovementRepository) CompleteReview(ctx context.Context, review *agentdomain.ImprovementReview, proposals []agentdomain.ChangeProposal) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for i := range proposals {
			proposals[i].CreatedAt, proposals[i].UpdatedAt = now, now
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&proposals[i]).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&agentdomain.ImprovementReview{}).Where("id = ? AND lease_token = ? AND status = ?", review.ID, review.LeaseToken, agentdomain.ReviewStatusRunning).
			Updates(map[string]any{"status": agentdomain.ReviewStatusCompleted, "completed_at": now, "lease_expires_at": nil, "error_message": "", "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentdomain.ErrLeaseLost
		}
		return nil
	})
}

func (r *AgentImprovementRepository) FailReview(ctx context.Context, review *agentdomain.ImprovementReview, cause error, retryAt *time.Time) error {
	status := agentdomain.ReviewStatusFailed
	if retryAt != nil && review.AttemptCount < review.MaxAttempts {
		status = agentdomain.ReviewStatusPending
	}
	result := r.db.WithContext(ctx).Model(&agentdomain.ImprovementReview{}).
		Where("id = ? AND lease_token = ?", review.ID, review.LeaseToken).
		Updates(map[string]any{"status": status, "retry_at": retryAt, "lease_expires_at": nil, "error_message": cause.Error(), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentdomain.ErrLeaseLost
	}
	return nil
}

func (r *AgentImprovementRepository) ListReviews(ctx context.Context, ownerID, agentID int64, limit int) ([]agentdomain.ImprovementReview, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var items []agentdomain.ImprovementReview
	err := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ?", ownerID, agentID).Order("id DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *AgentImprovementRepository) ListProposals(ctx context.Context, ownerID, agentID int64, status string, limit int) ([]agentdomain.ChangeProposal, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := r.db.WithContext(ctx).Where("owner_id = ? AND agent_id = ?", ownerID, agentID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var items []agentdomain.ChangeProposal
	err := query.Order("id DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *AgentImprovementRepository) FindProposal(ctx context.Context, ownerID, id int64) (*agentdomain.ChangeProposal, error) {
	var item agentdomain.ChangeProposal
	if err := r.db.WithContext(ctx).Where("owner_id = ? AND id = ?", ownerID, id).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AgentImprovementRepository) UpdateProposal(ctx context.Context, item *agentdomain.ChangeProposal) error {
	item.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(item).Error
}

var _ agentdomain.ImprovementRepository = (*AgentImprovementRepository)(nil)
