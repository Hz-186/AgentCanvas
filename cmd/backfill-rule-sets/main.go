package main

import (
	"context"
	"log"
	"os"

	"agentcanvas/internal/application/rule_backfill_usecase"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	"agentcanvas/internal/pkg/config"
)

func main() {
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
	result, err := service.Run(context.Background())
	log.Printf("legacy rule backfill finished: scanned=%d imported=%d skipped=%d failed=%d", result.Scanned, result.Imported, result.Skipped, result.Failed)
	if err != nil {
		log.Fatalf("legacy rule backfill completed with errors: %v", err)
	}
}
