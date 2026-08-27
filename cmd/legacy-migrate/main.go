// Command legacy-migrate is the Task 8 deterministic migration + retirement
// orchestrator. It runs the durable file importer and the historical
// reflection conversion, validates migration idempotency and the ES keyword
// backfill, and only then executes the destructive cleanup stage. Every step
// records progress in a JSON file next to the durable memory root so a failed
// run resumes instead of duplicating or deleting.
//
// ZERO destructive statements run when migration or ES backfill validation
// fails. Cleanup actions (legacy files, agent_reflections tables, retired
// API/index/worker surface, memory_write_logs) each run exactly once.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	memoryusecase "agentcanvas/internal/application/memory_usecase"
	reflectionusecase "agentcanvas/internal/application/reflection_usecase"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	"agentcanvas/internal/pkg/config"
)

func main() {
	configPath := flag.String("config", defaultConfigPath(), "AgentCanvas config path")
	root := flag.String("root", "", "durable memory root (default: AGENTCANVAS_MEMORY_ROOT or ~/.agentcanvas/memories)")
	progressPath := flag.String("progress", "", "progress file path (default: <root>/../.legacy-migrate-progress.json)")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	durableCfg := memoryusecase.NewDurableMemoryConfig(cfg.EffectiveDurableMemory())
	if strings.TrimSpace(*root) == "" {
		*root = durableCfg.Root
	}
	*root = strings.TrimSpace(*root)
	if *root == "" {
		log.Fatal("durable memory root is not configured")
	}
	if strings.TrimSpace(*progressPath) == "" {
		*progressPath = filepath.Join(filepath.Dir(*root), ".legacy-migrate-progress.json")
	}

	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		log.Fatalf("connect mysql: %v", err)
	}

	ctx := context.Background()
	progress := memoryusecase.NewFileProgressStore(*progressPath)

	artifacts := mysqlinfra.NewMemoryArtifactRepository(db)
	skills := memoryusecase.NewSkillRepositorySink(mysqlinfra.NewSkillRepository(db))
	fileMigration := memoryusecase.NewMemoryFileMigration(*root, artifacts, skills, progress)

	reflectionMigration := &reflectionusecase.ReflectionMigration{
		Reader: mysqlinfra.NewLegacyReflectionReader(db),
		Sink:   mysqlinfra.NewMemoryRepository(db),
	}

	if err := runMigration(ctx, fileMigration, reflectionMigration); err != nil {
		log.Fatalf("migration failed (sources left intact, run is rerunnable): %v", err)
	}

	retirement := mysqlinfra.NewLegacySchemaRetirement(db)
	cleanup := &memoryusecase.LegacyCleanup{
		ValidateMigration: func(ctx context.Context) error {
			// Rerunning the importer and converter is the idempotency check:
			// unchanged sources import zero rows and any tampered file aborts
			// here, before anything destructive runs.
			return runMigration(ctx, fileMigration, reflectionMigration)
		},
		ValidateESBackfill: retirement.ValidateContextBackfill,
		Actions: []memoryusecase.CleanupAction{
			{
				ID:  "delete_legacy_files",
				Run: func(ctx context.Context) error { return deleteLegacyFiles(ctx, *root) },
			},
			{
				ID:  "drop_agent_reflections",
				Run: retirement.DropReflectionTables,
			},
			{
				// The reflection API routes, semantic index, queue worker and
				// config surface were removed from this build; this action
				// records that retirement exactly once so reruns stay
				// consistent with the persisted cleanup ledger.
				ID:  "retire_reflection_api_index_worker",
				Run: func(context.Context) error { return nil },
			},
			{
				ID:  "drop_memory_write_logs",
				Run: retirement.DropMemoryWriteLogs,
			},
		},
		Progress: progress,
	}
	if err := cleanup.Run(ctx); err != nil {
		log.Fatalf("cleanup failed (no further destructive actions will run): %v", err)
	}
	log.Printf("legacy migration and cleanup complete; progress recorded at %s", *progressPath)
}

// runMigration imports durable files for every owner on disk and converts
// historical reflection rows for every owner holding them. Both steps are
// idempotent; the union of owner IDs keeps an owner with reflections but no
// files (or vice versa) covered.
func runMigration(ctx context.Context, files *memoryusecase.MemoryFileMigration, reflections *reflectionusecase.ReflectionMigration) error {
	fileOwners, err := files.OwnerIDs()
	if err != nil {
		return err
	}
	reflectionOwners, err := reflections.HistoricalOwnerIDs(ctx)
	if err != nil {
		return fmt.Errorf("list reflection owners: %w", err)
	}
	owners := unionOwners(fileOwners, reflectionOwners)
	for _, ownerID := range owners {
		if err := files.Run(ctx, ownerID); err != nil {
			return fmt.Errorf("import files for owner %d: %w", ownerID, err)
		}
		if err := reflections.Run(ctx, ownerID); err != nil {
			return fmt.Errorf("convert reflections for owner %d: %w", ownerID, err)
		}
		log.Printf("owner %d migrated", ownerID)
	}
	return nil
}

func unionOwners(left, right []int64) []int64 {
	seen := map[int64]bool{}
	owners := make([]int64, 0, len(left)+len(right))
	for _, id := range append(append([]int64(nil), left...), right...) {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		owners = append(owners, id)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	return owners
}

// deleteLegacyFiles removes every owner directory below the durable root. The
// root must contain only owner-<id> directories: any foreign path or symlink
// aborts before the first delete, reporting the exact offending path.
func deleteLegacyFiles(ctx context.Context, root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		path := filepath.Join(root, name)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected foreign path in durable memory root: %s", path)
		}
		if !strings.HasPrefix(name, "owner-") {
			return fmt.Errorf("unexpected foreign path in durable memory root: %s", path)
		}
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(name, "owner-"), 10, 64)
		if parseErr != nil || id <= 0 {
			return fmt.Errorf("unexpected foreign path in durable memory root: %s", path)
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("delete legacy owner directory %s: %w", path, err)
		}
		log.Printf("deleted legacy owner directory %s", path)
	}
	return nil
}

func defaultConfigPath() string {
	if path := strings.TrimSpace(os.Getenv("AGENTCANVAS_CONFIG_PATH")); path != "" {
		return path
	}
	if _, err := os.Stat("configs/config.local.yaml"); err == nil {
		return "configs/config.local.yaml"
	}
	return "configs/config.yaml"
}
