package api

import (
	"agentcanvas/internal/bootstrap"
	"agentcanvas/internal/pkg/config"
	"agentcanvas/internal/pkg/logger"
	"context"
	"fmt"
	"log"
	"os"
)

func main() {
	configPath := os.Getenv("AGENTCANVAS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.local.yaml"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("load config error: %v", err)
	}

	appLogger := logger.New(cfg.App.Env)
	app, err := bootstrap.NewApp(context.Background(), cfg, appLogger)
	if err != nil {
		appLogger.Error("init app failed", "error", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%d", cfg.App.Port)
	appLogger.Info("api server started", "addr", addr)

	if err := app.Router.Run(addr); err != nil {
		appLogger.Error("api server stopped", "error", err)
		os.Exit(1)
	}
}
