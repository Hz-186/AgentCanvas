package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/dialog"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	"agentcanvas/internal/pkg/config"

	"gorm.io/gorm"
)

type result struct{ Scanned, Created, Conversations, Skipped, Failed int }

func main() {
	dryRun := flag.Bool("dry-run", false, "report legacy Dialog migration without writing changes")
	flag.Parse()
	configPath := os.Getenv("AGENTCANVAS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.local.yaml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configPath = "configs/config.yaml"
		}
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	stats, err := run(context.Background(), db, *dryRun)
	log.Printf("legacy Dialog backfill finished: dry_run=%t scanned=%d created=%d conversations=%d skipped=%d failed=%d", *dryRun, stats.Scanned, stats.Created, stats.Conversations, stats.Skipped, stats.Failed)
	if err != nil {
		log.Fatalf("backfill failed: %v", err)
	}
}

func run(ctx context.Context, db *gorm.DB, dryRun bool) (result, error) {
	var dialogs []dialog.Dialog
	if err := db.WithContext(ctx).Where("deleted_at IS NULL").Order("id ASC").Find(&dialogs).Error; err != nil {
		return result{}, err
	}
	stats := result{Scanned: len(dialogs)}
	for _, legacy := range dialogs {
		var count int64
		if err := db.WithContext(ctx).Model(&agentdomain.Agent{}).Where("owner_id = ? AND legacy_dialog_id = ?", legacy.OwnerID, legacy.ID).Count(&count).Error; err != nil {
			stats.Failed++
			continue
		}
		if count > 0 {
			stats.Skipped++
			continue
		}
		if dryRun {
			stats.Created++
			continue
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var kbIDs []int64
			_ = json.Unmarshal([]byte(legacy.KBIDsJSON), &kbIDs)
			definition := agentdomain.Definition{ProviderID: legacy.ProviderID, Model: legacy.Model, SystemPrompt: legacy.SystemPrompt,
				Mode: "react", KnowledgeIDs: kbIDs, KnowledgeTopK: legacy.TopK, KnowledgeMode: legacy.RetrievalMode,
				MemoryEnabled: true, ReflectionEnabled: true}.Normalize()
			raw, checksum, err := definition.Snapshot()
			if err != nil {
				return err
			}
			resources, ruleHash, toolHash, err := definition.ResourceSnapshot()
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			agentItem := &agentdomain.Agent{OwnerID: legacy.OwnerID, Name: legacy.Name, Description: legacy.Description,
				Status: agentdomain.StatusActive, DraftDefinitionJSON: raw, LegacyDialogID: &legacy.ID, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(agentItem).Error; err != nil {
				return err
			}
			release := &agentdomain.Release{OwnerID: legacy.OwnerID, AgentID: agentItem.ID, VersionNo: 1, DefinitionJSON: raw,
				Checksum: checksum, RuleSetHash: ruleHash, ToolSchemaHash: toolHash, ResourceVersions: resources, CreatedBy: legacy.OwnerID, CreatedAt: now}
			if err := tx.Create(release).Error; err != nil {
				return err
			}
			if err := tx.Model(agentItem).Updates(map[string]any{"current_release_id": release.ID}).Error; err != nil {
				return err
			}
			update := tx.Table("conversations").Where("owner_id = ? AND dialog_id = ? AND deleted_at IS NULL", legacy.OwnerID, legacy.ID).
				Updates(map[string]any{"agent_id": agentItem.ID, "agent_release_id": release.ID, "source": "agent", "updated_at": now})
			if update.Error != nil {
				return update.Error
			}
			stats.Conversations += int(update.RowsAffected)
			return nil
		})
		if err != nil {
			stats.Failed++
			continue
		}
		stats.Created++
	}
	return stats, nil
}
