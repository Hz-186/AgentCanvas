package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	gitinfra "agentcanvas/internal/infrastructure/git"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	"agentcanvas/internal/pkg/config"
	"agentcanvas/internal/pkg/logger"
)

const workspacePruneInterval = time.Hour

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
		log.Fatalf("load config error: %v", err)
	}
	appLogger := logger.New(cfg.App.Env)
	if !cfg.GitWorkspace.Enabled {
		appLogger.Info("workspace pruner disabled")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		appLogger.Error("open mysql failed", "error", err)
		os.Exit(1)
	}
	if err := mysqlinfra.Ping(ctx, db); err != nil {
		appLogger.Error("ping mysql failed", "error", err)
		os.Exit(1)
	}

	gitService := gitinfra.NewService(gitinfra.Config{
		CommandTimeout:  time.Duration(cfg.GitWorkspace.GitCommandTimeoutSeconds) * time.Second,
		FetchTimeout:    time.Duration(cfg.GitWorkspace.FetchTimeoutSeconds) * time.Second,
		FetchFreshness:  time.Duration(cfg.GitWorkspace.FetchFreshnessSeconds) * time.Second,
		MaxOutputBytes:  cfg.GitWorkspace.MaxOutputBytes,
		WorktreeDirName: cfg.GitWorkspace.WorktreeDirName,
		GitUserName:     cfg.GitWorkspace.GitUserName,
		GitUserEmail:    cfg.GitWorkspace.GitUserEmail,
	})
	service := workspaceusecase.NewService(
		mysqlinfra.NewProjectRepository(db),
		mysqlinfra.NewWorkspaceRepository(db),
		gitService,
		workspaceusecase.Config{
			Enabled:                 cfg.GitWorkspace.Enabled,
			AllowedRoots:            cfg.GitWorkspace.AllowedRoots,
			WorktreeDirName:         cfg.GitWorkspace.WorktreeDirName,
			MaxWorkspacesPerProject: cfg.GitWorkspace.MaxWorkspacesPerProject,
			PruneTTL:                time.Duration(cfg.GitWorkspace.PruneTTLHours) * time.Hour,
			PreserveDirty:           cfg.GitWorkspace.PreserveDirty,
			PreserveUnpushed:        cfg.GitWorkspace.PreserveUnpushed,
			AutoInitRepository:      cfg.GitWorkspace.AutoInitRepository,
		},
	)
	service.ConfigureAudits(mysqlinfra.NewAuditRepository(db))

	prune := func() {
		if err := service.PruneStaleWorkspaces(ctx); err != nil {
			appLogger.Error("workspace prune failed", "error", err)
			return
		}
		appLogger.Info("workspace prune completed")
	}
	prune()
	ticker := time.NewTicker(workspacePruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			appLogger.Info("workspace pruner stopped")
			return
		case <-ticker.C:
			prune()
		}
	}
}
