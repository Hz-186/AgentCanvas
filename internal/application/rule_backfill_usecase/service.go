package rule_backfill_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/runtime/harness/rules"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	legacyCompilerModel = "legacy-import"
	legacyPromptVersion = "legacy-import-v1"
	defaultBatchSize    = 200
)

type Result struct {
	Scanned  int
	Imported int
	Skipped  int
	Failed   int
}

type Service struct {
	db        *gorm.DB
	batchSize int
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, batchSize: defaultBatchSize}
}

// Run imports legacy context_policy_json.rules into immutable published rule
// sets. Each profile is migrated atomically and failures do not stop unrelated
// tenants from being processed.
func (s *Service) Run(ctx context.Context) (Result, error) {
	var result Result
	if s == nil || s.db == nil {
		return result, fmt.Errorf("legacy rule backfill database is not configured")
	}
	batchSize := s.batchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	var failures []error
	var cursor int64
	for {
		var profiles []workflow.Profile
		err := s.db.WithContext(ctx).
			Where("id > ? AND active_rule_set_id IS NULL AND deleted_at IS NULL", cursor).
			Order("id ASC").Limit(batchSize).Find(&profiles).Error
		if err != nil {
			return result, fmt.Errorf("list legacy workflow profiles: %w", err)
		}
		if len(profiles) == 0 {
			break
		}
		for index := range profiles {
			profile := &profiles[index]
			cursor = profile.ID
			result.Scanned++
			items, err := decodeLegacyRules(profile.ContextPolicyJSON)
			if err != nil {
				result.Failed++
				failures = append(failures, fmt.Errorf("profile %d: %w", profile.ID, err))
				continue
			}
			if len(items) == 0 {
				result.Skipped++
				continue
			}
			imported, err := s.importProfile(ctx, profile.ID, items)
			if err != nil {
				result.Failed++
				failures = append(failures, fmt.Errorf("profile %d: %w", profile.ID, err))
				continue
			}
			if imported {
				observability.RuleSystemMetrics.RecordPublished()
				result.Imported++
			} else {
				result.Skipped++
			}
		}
	}
	return result, errors.Join(failures...)
}

func (s *Service) importProfile(ctx context.Context, profileID int64, items []rules.Rule) (bool, error) {
	if err := rules.ValidateCustomRules(items); err != nil {
		return false, fmt.Errorf("legacy rules are invalid: %w", err)
	}
	sourceHash := legacySourceHash(items)
	imported := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var profile workflow.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", profileID).First(&profile).Error; err != nil {
			return err
		}
		if profile.ActiveRuleSetID != nil && *profile.ActiveRuleSetID > 0 {
			return nil
		}

		idempotencyKey := fmt.Sprintf("legacy-backfill:%d", profile.ID)
		var priorJob workflow.RuleCompileJob
		err := tx.Where("owner_id = ? AND workflow_id = ? AND idempotency_key = ?", profile.OwnerID, profile.WorkflowID, idempotencyKey).
			First(&priorJob).Error
		if err == nil {
			var priorSet workflow.RuleSet
			if err := tx.Where("id = ? AND owner_id = ? AND workflow_id = ? AND status = ?", priorJob.RuleSetID, profile.OwnerID, profile.WorkflowID, workflow.RuleSetStatusPublished).
				First(&priorSet).Error; err != nil {
				return fmt.Errorf("legacy backfill has an incomplete prior attempt: %w", err)
			}
			return tx.Model(&workflow.Profile{}).Where("id = ? AND active_rule_set_id IS NULL", profile.ID).
				Update("active_rule_set_id", priorSet.ID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var latestVersion int
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ?", profile.OwnerID, profile.WorkflowID).
			Select("COALESCE(MAX(version_no), 0)").Scan(&latestVersion).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		set := workflow.RuleSet{
			OwnerID: profile.OwnerID, WorkflowID: profile.WorkflowID,
			VersionNo: latestVersion + 1, Status: workflow.RuleSetStatusDraft, Revision: 1,
			SourceHash: sourceHash, CompilerModel: legacyCompilerModel,
			CompilerPromptVersion: legacyPromptVersion,
			TokenEstimatorVersion: rules.DefaultTokenEstimatorVersion,
			CreatedAt:             now, UpdatedAt: now,
		}
		if err := tx.Create(&set).Error; err != nil {
			return err
		}

		compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{
			RuleSetID: set.ID, Version: strconv.Itoa(set.VersionNo),
			RejectLegacyPermanentLevels: true,
		})
		if err != nil {
			return err
		}
		snapshot, err := json.Marshal(compiled)
		if err != nil {
			return err
		}
		nodes, edges, err := legacyRows(compiled)
		if err != nil {
			return err
		}
		for index := range nodes {
			nodes[index].RuleSetID = set.ID
		}
		if len(nodes) > 0 {
			if err := tx.Create(&nodes).Error; err != nil {
				return err
			}
		}
		for index := range edges {
			edges[index].RuleSetID = set.ID
		}
		if len(edges) > 0 {
			if err := tx.Create(&edges).Error; err != nil {
				return err
			}
		}

		job := workflow.RuleCompileJob{
			OwnerID: profile.OwnerID, WorkflowID: profile.WorkflowID, RuleSetID: set.ID,
			Revision: set.Revision, SourceHash: sourceHash,
			Status: workflow.RuleCompileJobCompleted, Attempts: 1,
			IdempotencyKey: idempotencyKey, CompilerModel: legacyCompilerModel,
			AvailableAt: now, StartedAt: &now, FinishedAt: &now, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		if err := tx.Model(&workflow.RuleSet{}).
			Where("owner_id = ? AND workflow_id = ? AND status = ? AND id <> ?", profile.OwnerID, profile.WorkflowID, workflow.RuleSetStatusPublished, set.ID).
			Update("status", workflow.RuleSetStatusSuperseded).Error; err != nil {
			return err
		}
		publishedBy := profile.OwnerID
		set.Status = workflow.RuleSetStatusPublished
		set.CompiledSnapshotJSON = snapshot
		set.CompiledHash = compiled.CompiledHash
		set.PublishedBy = &publishedBy
		set.PublishedAt = &now
		set.UpdatedAt = now
		if err := tx.Save(&set).Error; err != nil {
			return err
		}
		updated := tx.Model(&workflow.Profile{}).
			Where("id = ? AND active_rule_set_id IS NULL", profile.ID).
			Update("active_rule_set_id", set.ID)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return workflow.ErrRuleSetConflict
		}
		imported = true
		return nil
	})
	return imported, err
}

func decodeLegacyRules(raw json.RawMessage) ([]rules.Rule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var policy struct {
		Rules []rules.Rule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, fmt.Errorf("decode context_policy_json: %w", err)
	}
	return policy.Rules, nil
}

func legacySourceHash(items []rules.Rule) string {
	cloned := append([]rules.Rule(nil), items...)
	sort.SliceStable(cloned, func(i, j int) bool { return cloned[i].ID < cloned[j].ID })
	data, _ := json.Marshal(cloned)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func legacyRows(compiled *rules.CompiledRuleSet) ([]workflow.RuleNode, []workflow.RuleEdge, error) {
	nodes := make([]workflow.RuleNode, 0, len(compiled.Rules))
	edges := make([]workflow.RuleEdge, 0)
	for _, item := range compiled.Rules {
		activation, err := json.Marshal(item.Rule.Activation)
		if err != nil {
			return nil, nil, err
		}
		var binding json.RawMessage
		if item.Rule.PolicyBinding != nil {
			binding, err = json.Marshal(item.Rule.PolicyBinding)
			if err != nil {
				return nil, nil, err
			}
		}
		nodes = append(nodes, workflow.RuleNode{
			RuleID: item.Rule.ID, Name: item.Rule.Name, Content: item.Rule.Content,
			Strength: string(item.Rule.EffectiveStrength()), ActivationJSON: activation,
			Priority: item.Rule.Priority, SafetyCritical: item.Rule.SafetyCritical,
			PolicyBindingJSON: binding, TokenCost: item.TokenCost,
			TopologicalOrder: item.TopologicalOrder, ContentHash: item.ContentHash,
		})
		for _, dependency := range item.DependsOn {
			edges = append(edges, workflow.RuleEdge{
				RuleID: item.Rule.ID, DependsOnRuleID: dependency,
				Source: "manual", Decision: workflow.RuleEdgeDecisionAccepted,
			})
		}
	}
	return nodes, edges, nil
}
