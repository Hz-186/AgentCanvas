package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"agentcanvas/internal/domain/contextresource"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	"agentcanvas/internal/pkg/config"
)

func main() {
	configPath := flag.String("config", defaultConfigPath(), "AgentCanvas config path")
	dryRun := flag.Bool("dry-run", false, "scan without writing outbox events")
	resourceType := flag.String("resource-type", "", "resume a single resource type")
	afterID := flag.Int64("after-id", 0, "resume after this resource id")
	batchSize := flag.Int("batch-size", 500, "rows per transaction")
	embeddingProviderID := flag.Int64("embedding-provider-id", 0, "override embedding provider for a new index version")
	embeddingModel := flag.String("embedding-model", "", "override embedding model for a new index version")
	embeddingDimensions := flag.Int("embedding-dimensions", 0, "expected dimensions for a new index version")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		log.Fatalf("connect mysql: %v", err)
	}
	repo := mysqlinfra.NewContextResourceRepository(db)
	types := contextresource.AllResourceTypes
	if strings.TrimSpace(*resourceType) != "" {
		types = []string{strings.TrimSpace(*resourceType)}
	}
	ctx := contextresource.WithEmbeddingProfile(context.Background(), contextresource.EmbeddingProfile{ProviderID: *embeddingProviderID, Model: *embeddingModel, Dimensions: *embeddingDimensions})
	for _, currentType := range types {
		cursor := int64(0)
		if len(types) == 1 {
			cursor = *afterID
		}
		for {
			result, err := repo.Backfill(ctx, currentType, cursor, *batchSize, *dryRun)
			if err != nil {
				log.Fatalf("backfill %s after %d: %v", currentType, cursor, err)
			}
			encoded, _ := json.Marshal(result)
			fmt.Println(string(encoded))
			if result.Done {
				break
			}
			cursor = result.NextID
		}
	}
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
