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

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/runtime/harness/rules"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultBatchSize = 200

type Result struct {
	Scanned        int
	Converted      int
	Ignored        int
	WouldImport    int
	Imported       int
	Skipped        int
	Failed         int
	Recompiled     int
	AgentsMigrated int
}

type Service struct {
	db          *gorm.DB
	batchSize   int
	dryRun      bool
	removeGraph bool
}

func (s *Service) SetDryRun(enabled bool) { s.dryRun = enabled }

func (s *Service) SetRemoveGraph(enabled bool) { s.removeGraph = enabled }

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, batchSize: defaultBatchSize}
}

func (s *Service) Run(ctx context.Context) (Result, error) {
	if s.removeGraph {
		return s.runGraphRemoval(ctx)
	}
	return s.runLegacyImport(ctx)
}

func (s *Service) runLegacyImport(ctx context.Context) (Result, error) {
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
		err := s.db.WithContext(ctx).Where("id > ? AND active_rule_set_id IS NULL AND deleted_at IS NULL", cursor).
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
			items, ignored, err := decodeLegacyRules(profile.ContextPolicyJSON)
			if err != nil {
				result.Failed++
				failures = append(failures, fmt.Errorf("profile %d: %w", profile.ID, err))
				continue
			}
			result.Converted += len(items)
			result.Ignored += len(ignored)
			if len(items) == 0 {
				result.Skipped++
				continue
			}
			if err := rules.ValidateCustomRules(items); err != nil {
				result.Failed++
				failures = append(failures, fmt.Errorf("profile %d: %w", profile.ID, err))
				continue
			}
			if s.dryRun {
				result.WouldImport++
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
	imported := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var profile workflow.Profile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleted_at IS NULL", profileID).First(&profile).Error; err != nil {
			return err
		}
		if profile.ActiveRuleSetID != nil && *profile.ActiveRuleSetID > 0 {
			return nil
		}
		var latestVersion int
		if err := tx.Model(&workflow.RuleSet{}).Where("owner_id = ? AND workflow_id = ?", profile.OwnerID, profile.WorkflowID).
			Select("COALESCE(MAX(version_no), 0)").Scan(&latestVersion).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		set := workflow.RuleSet{
			OwnerID: profile.OwnerID, WorkflowID: profile.WorkflowID, VersionNo: latestVersion + 1,
			Status: workflow.RuleSetStatusDraft, Revision: 1, SourceHash: legacySourceHash(items),
			TokenEstimatorVersion: rules.DefaultTokenEstimatorVersion, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&set).Error; err != nil {
			return err
		}
		compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{RuleSetID: set.ID, Version: strconv.Itoa(set.VersionNo)})
		if err != nil {
			return err
		}
		snapshot, err := json.Marshal(compiled)
		if err != nil {
			return err
		}
		nodes, err := compiledRows(compiled)
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
		if err := tx.Model(&workflow.RuleSet{}).Where("owner_id = ? AND workflow_id = ? AND status = ? AND id <> ?", profile.OwnerID, profile.WorkflowID, workflow.RuleSetStatusPublished, set.ID).
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
		updated := tx.Model(&workflow.Profile{}).Where("id = ? AND active_rule_set_id IS NULL", profile.ID).Update("active_rule_set_id", set.ID)
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

func (s *Service) runGraphRemoval(ctx context.Context) (Result, error) {
	var result Result
	if s == nil || s.db == nil {
		return result, fmt.Errorf("rule graph removal database is not configured")
	}
	var sets []workflow.RuleSet
	if err := s.db.WithContext(ctx).Where("status IN ?", []string{workflow.RuleSetStatusDraft, workflow.RuleSetStatusPublished, workflow.RuleSetStatusSuperseded}).Order("id ASC").Find(&sets).Error; err != nil {
		return result, err
	}
	var failures []error
	for index := range sets {
		set := &sets[index]
		result.Scanned++
		var nodes []workflow.RuleNode
		if err := s.db.WithContext(ctx).Where("rule_set_id = ?", set.ID).Order("rule_id ASC").Find(&nodes).Error; err != nil {
			result.Failed++
			failures = append(failures, fmt.Errorf("rule set %d: %w", set.ID, err))
			continue
		}
		items, err := rulesFromNodes(nodes)
		if err != nil {
			result.Failed++
			failures = append(failures, fmt.Errorf("rule set %d: %w", set.ID, err))
			continue
		}
		items, err = mergeSnapshotTriggers(items, set.CompiledSnapshotJSON)
		if err != nil {
			result.Failed++
			failures = append(failures, fmt.Errorf("rule set %d: restore legacy triggers: %w", set.ID, err))
			continue
		}
		compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{RuleSetID: set.ID, Version: strconv.Itoa(set.VersionNo)})
		if err != nil {
			result.Failed++
			failures = append(failures, fmt.Errorf("rule set %d: %w", set.ID, err))
			continue
		}
		if set.Status == workflow.RuleSetStatusDraft {
			result.Skipped++
			continue
		}
		result.WouldImport++
		if s.dryRun {
			continue
		}
		if err := s.persistGraphFreeSnapshot(ctx, set, compiled); err != nil {
			result.Failed++
			failures = append(failures, fmt.Errorf("rule set %d: %w", set.ID, err))
			continue
		}
		result.Recompiled++
	}
	if result.Failed > 0 {
		return result, errors.Join(failures...)
	}
	agentCount, err := s.migrateAgents(ctx)
	if err != nil {
		return result, err
	}
	result.AgentsMigrated = agentCount
	return result, nil
}

func (s *Service) persistGraphFreeSnapshot(ctx context.Context, set *workflow.RuleSet, compiled *rules.CompiledRuleSet) error {
	snapshot, err := json.Marshal(compiled)
	if err != nil {
		return err
	}
	compiledNodes, err := compiledRows(compiled)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&workflow.RuleSet{}).Where("id = ?", set.ID).Updates(map[string]any{
			"source_hash": legacySourceHash(rules.RulesFromCompiled(compiled)), "compiled_hash": compiled.CompiledHash,
			"compiled_snapshot_json": snapshot, "token_estimator_version": compiled.TokenEstimatorVersion, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		for _, node := range compiledNodes {
			if err := tx.Model(&workflow.RuleNode{}).Where("rule_set_id = ? AND rule_id = ?", set.ID, node.RuleID).
				Updates(map[string]any{"triggers_json": node.TriggersJSON, "token_cost": node.TokenCost, "content_hash": node.ContentHash, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) migrateAgents(ctx context.Context) (int, error) {
	var agents []agentdomain.Agent
	if err := s.db.WithContext(ctx).Where("deleted_at IS NULL").Order("id ASC").Find(&agents).Error; err != nil {
		return 0, err
	}
	migrated := 0
	for index := range agents {
		changed, err := s.migrateAgent(ctx, &agents[index])
		if err != nil {
			return migrated, fmt.Errorf("agent %d: %w", agents[index].ID, err)
		}
		if changed {
			migrated++
		}
	}
	return migrated, nil
}

func (s *Service) migrateAgent(ctx context.Context, item *agentdomain.Agent) (bool, error) {
	var draft agentdomain.Definition
	if err := json.Unmarshal(item.DraftDefinitionJSON, &draft); err != nil {
		return false, err
	}
	cleanDraftRules, draftChanged, err := stripRuleGraphJSON(draft.RulesJSON)
	if err != nil {
		return false, err
	}
	draft.RulesJSON = cleanDraftRules
	var current *agentdomain.Release
	var cleanRelease agentdomain.Definition
	releaseChanged := false
	if item.CurrentReleaseID != nil && *item.CurrentReleaseID > 0 {
		var release agentdomain.Release
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND agent_id = ?", *item.CurrentReleaseID, item.OwnerID, item.ID).First(&release).Error; err != nil {
			return false, err
		}
		if err := json.Unmarshal(release.DefinitionJSON, &cleanRelease); err != nil {
			return false, err
		}
		cleanRelease.RulesJSON, releaseChanged, err = stripRuleGraphJSON(cleanRelease.RulesJSON)
		if err != nil {
			return false, err
		}
		current = &release
	}
	if !draftChanged && !releaseChanged {
		return false, nil
	}
	if s.dryRun {
		return true, nil
	}
	return true, s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if draftChanged {
			raw, err := json.Marshal(draft.Normalize())
			if err != nil {
				return err
			}
			if err := tx.Model(&agentdomain.Agent{}).Where("id = ? AND owner_id = ?", item.ID, item.OwnerID).
				Updates(map[string]any{"draft_definition_json": raw, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if !releaseChanged || current == nil {
			return nil
		}
		definitionJSON, checksum, err := cleanRelease.Snapshot()
		if err != nil {
			return err
		}
		resources, ruleHash, toolHash, err := cleanRelease.ResourceSnapshot()
		if err != nil {
			return err
		}
		var latest int
		if err := tx.Model(&agentdomain.Release{}).Where("agent_id = ?", item.ID).Select("COALESCE(MAX(version_no), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		release := agentdomain.Release{
			OwnerID: item.OwnerID, AgentID: item.ID, VersionNo: latest + 1, DefinitionJSON: definitionJSON,
			Checksum: checksum, RuleSetHash: ruleHash, ToolSchemaHash: toolHash, ResourceVersions: resources,
			CreatedBy: item.OwnerID, CreatedAt: time.Now().UTC(),
		}
		if err := tx.Create(&release).Error; err != nil {
			return err
		}
		return tx.Model(&agentdomain.Agent{}).Where("id = ? AND current_release_id = ?", item.ID, current.ID).
			Updates(map[string]any{"current_release_id": release.ID, "updated_at": time.Now().UTC()}).Error
	})
}

func stripRuleGraphJSON(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return raw, false, nil
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false, err
	}
	changed := false
	for index := range items {
		if _, ok := items[index]["manual_depends_on"]; ok {
			delete(items[index], "manual_depends_on")
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	cleaned, err := json.Marshal(items)
	if err != nil {
		return nil, false, err
	}
	var decoded []rules.Rule
	if err := json.Unmarshal(cleaned, &decoded); err != nil {
		return nil, false, err
	}
	if _, err := rules.CompileRuntimeRuleSet(decoded); err != nil {
		return nil, false, err
	}
	return cleaned, true, nil
}

func decodeLegacyRules(raw json.RawMessage) ([]rules.Rule, []string, error) {
	return rules.DecodeLegacyPolicyRules(raw)
}

func rulesFromNodes(nodes []workflow.RuleNode) ([]rules.Rule, error) {
	items := make([]rules.Rule, 0, len(nodes))
	for _, node := range nodes {
		var activation rules.Activation
		if len(node.ActivationJSON) > 0 {
			if err := json.Unmarshal(node.ActivationJSON, &activation); err != nil {
				return nil, err
			}
		}
		var triggers []string
		if len(node.TriggersJSON) > 0 {
			if err := json.Unmarshal(node.TriggersJSON, &triggers); err != nil {
				return nil, err
			}
		}
		var binding *rules.PolicyBinding
		if len(node.PolicyBindingJSON) > 0 && string(node.PolicyBindingJSON) != "null" {
			binding = &rules.PolicyBinding{}
			if err := json.Unmarshal(node.PolicyBindingJSON, binding); err != nil {
				return nil, err
			}
		}
		items = append(items, rules.Rule{ID: node.RuleID, Name: node.Name, Strength: rules.RuleStrength(node.Strength), Content: node.Content, Triggers: triggers, Activation: activation, Priority: node.Priority, SafetyCritical: node.SafetyCritical, PolicyBinding: binding})
	}
	return items, nil
}

func mergeSnapshotTriggers(items []rules.Rule, snapshot json.RawMessage) ([]rules.Rule, error) {
	if len(snapshot) == 0 || string(snapshot) == "null" {
		return items, nil
	}
	var stored struct {
		Rules []struct {
			Rule rules.LegacyRuleDTO `json:"rule"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(snapshot, &stored); err != nil {
		return nil, err
	}
	triggersByID := make(map[string][]string, len(stored.Rules))
	for _, entry := range stored.Rules {
		if len(entry.Rule.Triggers) > 0 {
			triggersByID[entry.Rule.ID] = append([]string(nil), entry.Rule.Triggers...)
		}
	}
	merged := append([]rules.Rule(nil), items...)
	for index := range merged {
		if len(merged[index].Triggers) == 0 {
			merged[index].Triggers = append([]string(nil), triggersByID[merged[index].ID]...)
		}
	}
	return merged, nil
}

func legacySourceHash(items []rules.Rule) string {
	cloned := append([]rules.Rule(nil), items...)
	sort.SliceStable(cloned, func(i, j int) bool { return cloned[i].ID < cloned[j].ID })
	data, _ := json.Marshal(cloned)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func compiledRows(compiled *rules.CompiledRuleSet) ([]workflow.RuleNode, error) {
	nodes := make([]workflow.RuleNode, 0, len(compiled.Rules))
	for _, item := range compiled.Rules {
		activation, err := json.Marshal(item.Rule.Activation)
		if err != nil {
			return nil, err
		}
		triggers, err := json.Marshal(item.Rule.Triggers)
		if err != nil {
			return nil, err
		}
		var binding json.RawMessage
		if item.Rule.PolicyBinding != nil {
			binding, err = json.Marshal(item.Rule.PolicyBinding)
			if err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, workflow.RuleNode{RuleID: item.Rule.ID, Name: item.Rule.Name, Content: item.Rule.Content, Strength: string(item.Rule.Strength), ActivationJSON: activation, TriggersJSON: triggers, Priority: item.Rule.Priority, SafetyCritical: item.Rule.SafetyCritical, PolicyBindingJSON: binding, TokenCost: item.TokenCost, ContentHash: item.ContentHash})
	}
	return nodes, nil
}
