package main

import (
	"context"
	"flag"
	"log"
	"os"

	"agentcanvas/internal/application/rule_backfill_usecase"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	"agentcanvas/internal/pkg/config"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "validate and report legacy rules without writing changes")
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
	service := rule_backfill_usecase.NewService(db)
	service.SetDryRun(*dryRun)
	result, err := service.Run(context.Background())
	log.Printf("legacy rule backfill finished: dry_run=%t scanned=%d converted=%d ignored=%d would_import=%d imported=%d skipped=%d failed=%d", *dryRun, result.Scanned, result.Converted, result.Ignored, result.WouldImport, result.Imported, result.Skipped, result.Failed)
	if err != nil {
		log.Fatalf("legacy rule backfill completed with errors: %v", err)
	}
}
