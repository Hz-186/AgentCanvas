package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"
	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
	agenterrors "agentcanvas/internal/pkg/errors"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAgentRuntimeRunPersistenceIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTCANVAS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set AGENTCANVAS_TEST_MYSQL_DSN to run MySQL integration tests")
	}
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ownerID := int64(990031)
	runRepo := NewRunRepository(db)
	eventRepo := NewRunEventRepository(db)
	stepRepo := NewRunStepRepository(db)
	cleanup := func() {
		_ = db.Exec("DELETE FROM agent_run_steps WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM agent_run_events WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM agent_runs WHERE owner_id = ?", ownerID).Error
	}
	cleanup()
	t.Cleanup(cleanup)
	expectedColumns := map[string][]string{
		"agent_runs":              {"id", "owner_id", "agent_id", "agent_release_id", "conversation_id", "workspace_id", "parent_run_id", "run_type", "delegation_depth", "definition_json", "definition_hash", "rule_hash", "status", "input_json", "output_json", "error_message", "total_tokens", "latency_ms", "started_at", "finished_at", "created_at", "updated_at"},
		"agent_run_events":        {"id", "owner_id", "run_id", "event_type", "payload_json", "created_at"},
		"agent_run_steps":         {"id", "owner_id", "run_id", "step_index", "step_type", "role", "content", "tool_call_id", "tool_name", "arguments_json", "output_json", "compressed", "error_message", "token_count", "latency_ms", "provider_id", "model", "created_at"},
		"agent_run_checkpoints":   {"id", "owner_id", "run_id", "status", "snapshot_version", "interaction_id", "runtime_checkpoint_json", "messages_json", "messages_summary", "steps_json", "pending_tool_call_json", "context_json", "tool_registry_hash", "tool_policy_hash", "created_at", "updated_at"},
		"agent_approval_requests": {"id", "owner_id", "run_id", "tool_call_id", "interaction_id", "tool_name", "risk_level", "reason", "request_json", "status", "decision_note", "decided_at", "created_at", "updated_at"},
	}
	for table, expected := range expectedColumns {
		rows, queryErr := db.Raw("SELECT COLUMN_NAME FROM information_schema.columns WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION", table).Rows()
		if queryErr != nil {
			t.Fatalf("query columns for %s: %v", table, queryErr)
		}
		var actual []string
		for rows.Next() {
			var column string
			if scanErr := rows.Scan(&column); scanErr != nil {
				rows.Close()
				t.Fatalf("scan columns for %s: %v", table, scanErr)
			}
			actual = append(actual, column)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("read columns for %s: %v", table, err)
		}
		if !slices.Equal(actual, expected) {
			t.Fatalf("unexpected columns for %s: got %v want %v", table, actual, expected)
		}
	}
	now := time.Now().UTC()
	run := &agentdomain.Run{OwnerID: ownerID, AgentID: 7, RunType: agentdomain.RunTypeTurn, Status: agentdomain.RunStatusQueued,
		DefinitionJSON: []byte(`{"mode":"react"}`), DefinitionHash: "definition-hash", RuleHash: "rule-hash",
		InputJSON: []byte(`{"query":"integration"}`), StartedAt: now}
	if err := runRepo.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := eventRepo.Create(context.Background(), &agentdomain.RunEvent{OwnerID: ownerID, RunID: run.ID, EventType: "run.queued", PayloadJSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := stepRepo.Create(context.Background(), &agentdomain.RunStep{OwnerID: ownerID, RunID: run.ID, StepIndex: 1, StepType: "llm_response", ProviderID: 3, Model: "mock", TokenCount: 4, LatencyMS: 8}); err != nil {
		t.Fatal(err)
	}
	loaded, err := runRepo.FindByID(context.Background(), ownerID, run.ID)
	if err != nil || loaded.DefinitionHash != "definition-hash" || loaded.RuleHash != "rule-hash" || loaded.RunType != agentdomain.RunTypeTurn {
		t.Fatalf("run snapshot was not persisted: run=%+v err=%v", loaded, err)
	}
	events, err := eventRepo.ListByRun(context.Background(), ownerID, run.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("run events were not persisted: events=%+v err=%v", events, err)
	}
	steps, err := stepRepo.ListByRun(context.Background(), ownerID, run.ID)
	if err != nil || len(steps) != 1 || steps[0].ProviderID != 3 || steps[0].Model != "mock" {
		t.Fatalf("run step provider/model snapshot was not persisted: steps=%+v err=%v", steps, err)
	}
}

func TestProjectWorkspaceRepositoriesIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTCANVAS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set AGENTCANVAS_TEST_MYSQL_DSN to run MySQL integration tests")
	}
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ownerID := time.Now().UnixNano()
	cleanup := func() {
		_ = db.Exec("DELETE FROM agent_workspaces WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM project_folders WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM projects WHERE owner_id = ?", ownerID).Error
	}
	cleanup()
	t.Cleanup(cleanup)

	projects := NewProjectRepository(db)
	workspaces := NewWorkspaceRepository(db)
	projectItem := &projectdomain.Project{OwnerID: ownerID, Slug: "atomic-project", Name: "Atomic Project", PrimaryPath: "/tmp/atomic-project"}
	primaryFolder := &projectdomain.ProjectFolder{Label: "Primary"}
	if err := projects.CreateWithPrimaryFolder(ctx, projectItem, primaryFolder); err != nil {
		t.Fatal(err)
	}
	loaded, err := projects.FindByID(ctx, ownerID, projectItem.ID)
	if err != nil || len(loaded.Folders) != 1 || !loaded.Folders[0].IsPrimary || loaded.Folders[0].Path != projectItem.PrimaryPath {
		t.Fatalf("atomic project creation mismatch: project=%#v err=%v", loaded, err)
	}
	secondaryFolder := &projectdomain.ProjectFolder{OwnerID: ownerID, ProjectID: projectItem.ID, Path: "/tmp/atomic-project-secondary", Label: "Secondary"}
	if err := projects.AddFolder(ctx, secondaryFolder); err != nil {
		t.Fatal(err)
	}
	if err := projects.SetPrimaryFolder(ctx, ownerID, projectItem.ID, secondaryFolder.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = projects.FindByID(ctx, ownerID, projectItem.ID)
	if err != nil || loaded.PrimaryPath != secondaryFolder.Path || len(loaded.Folders) != 2 || !loaded.Folders[0].IsPrimary || loaded.Folders[0].ID != secondaryFolder.ID {
		t.Fatalf("primary folder switch mismatch: project=%#v err=%v", loaded, err)
	}
	if err := projects.SetPrimaryFolder(ctx, ownerID+1, projectItem.ID, secondaryFolder.ID); !errors.Is(err, agenterrors.ErrNotFound) {
		t.Fatalf("cross-owner primary folder switch error = %v, want not found", err)
	}
	if err := projects.SetPrimaryFolder(ctx, ownerID, projectItem.ID, secondaryFolder.ID+1_000_000); !errors.Is(err, agenterrors.ErrNotFound) {
		t.Fatalf("missing primary folder switch error = %v, want not found", err)
	}
	loaded, err = projects.FindByID(ctx, ownerID, projectItem.ID)
	if err != nil || loaded.PrimaryPath != secondaryFolder.Path || len(loaded.Folders) != 2 || !loaded.Folders[0].IsPrimary || loaded.Folders[0].ID != secondaryFolder.ID {
		t.Fatalf("failed primary switch changed the existing primary folder: project=%#v err=%v", loaded, err)
	}

	rolledBack := &projectdomain.Project{OwnerID: ownerID, Slug: "rolled-back", Name: "Rolled Back", PrimaryPath: "/tmp/rolled-back"}
	duplicateFolderID := &projectdomain.ProjectFolder{ID: primaryFolder.ID, Label: "duplicate primary key"}
	if err := projects.CreateWithPrimaryFolder(ctx, rolledBack, duplicateFolderID); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("atomic rollback error = %v, want conflict", err)
	}
	var rolledBackCount int64
	if err := db.Model(&projectdomain.Project{}).Where("owner_id = ? AND slug = ?", ownerID, rolledBack.Slug).Count(&rolledBackCount).Error; err != nil || rolledBackCount != 0 {
		t.Fatalf("project survived failed primary folder transaction: count=%d err=%v", rolledBackCount, err)
	}

	secondProject := &projectdomain.Project{OwnerID: ownerID, Slug: "second-project", Name: "Second", PrimaryPath: "/tmp/second-project"}
	secondFolder := &projectdomain.ProjectFolder{Label: "Primary"}
	if err := projects.CreateWithPrimaryFolder(ctx, secondProject, secondFolder); err != nil {
		t.Fatal(err)
	}
	secondProject.Slug = projectItem.Slug
	if err := projects.Update(ctx, secondProject); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("project update duplicate error = %v, want conflict", err)
	}

	firstWorkspace := &workspacedomain.Workspace{OwnerID: ownerID, ProjectID: projectItem.ID, RunID: ownerID + 1, Kind: workspacedomain.KindWorktree, RepositoryRoot: projectItem.PrimaryPath, WorkspacePath: projectItem.PrimaryPath + "/.worktrees/one", BranchName: "atomic/one", Status: workspacedomain.StatusReady}
	secondWorkspace := &workspacedomain.Workspace{OwnerID: ownerID, ProjectID: projectItem.ID, RunID: ownerID + 2, Kind: workspacedomain.KindWorktree, RepositoryRoot: projectItem.PrimaryPath, WorkspacePath: projectItem.PrimaryPath + "/.worktrees/two", BranchName: "atomic/two", Status: workspacedomain.StatusReady}
	if err := workspaces.Create(ctx, firstWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := workspaces.Create(ctx, secondWorkspace); err != nil {
		t.Fatal(err)
	}
	secondWorkspace.BranchName = firstWorkspace.BranchName
	if err := workspaces.Update(ctx, secondWorkspace); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("workspace update duplicate error = %v, want conflict", err)
	}
}

func TestGitWorkspaceMigrationRoundTripIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTCANVAS_TEST_MYSQL_ADMIN_DSN")
	if dsn == "" {
		t.Skip("set AGENTCANVAS_TEST_MYSQL_ADMIN_DSN to run migration up/down/up integration")
	}
	adminConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.DBName = ""
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	if err := adminDB.Ping(); err != nil {
		t.Fatal(err)
	}

	databaseName := fmt.Sprintf("agentcanvas_migration_test_%d", time.Now().UnixNano())
	quotedDatabase := "`" + databaseName + "`"
	if _, err := adminDB.Exec("CREATE DATABASE " + quotedDatabase + " CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + quotedDatabase) })
	testConfig := adminConfig.Clone()
	testConfig.DBName = databaseName
	testDB, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Close()

	migrationRoot := filepath.Join("..", "..", "..", "migrations")
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000001_agent_only_baseline.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000002_git_workspace.up.sql"))
	assertGitWorkspaceMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000002_git_workspace.down.sql"))
	assertGitWorkspaceMigrationState(t, testDB, false)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000002_git_workspace.up.sql"))
	assertGitWorkspaceMigrationState(t, testDB, true)
}

func applyIntegrationMigration(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range splitIntegrationSQLStatements(string(data)) {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("execute migration %s: %v\nstatement: %s", path, err, statement)
		}
	}
}

func splitIntegrationSQLStatements(sqlText string) []string {
	lines := strings.Split(sqlText, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	parts := strings.Split(strings.Join(cleaned, "\n"), ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		if statement := strings.TrimSpace(part); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func assertGitWorkspaceMigrationState(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()
	for _, table := range []string{"projects", "project_folders", "agent_workspaces"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != expected {
			t.Fatalf("table %s existence = %v, want %v", table, count == 1, expected)
		}
	}
	for table, columns := range map[string][]string{"conversations": {"project_id", "workspace_mode"}, "agent_runs": {"workspace_id"}} {
		for _, column := range columns {
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != expected {
				t.Fatalf("column %s.%s existence = %v, want %v", table, column, count == 1, expected)
			}
		}
	}
	if !expected {
		return
	}
	for _, index := range []string{"uk_projects_owner_slug", "uk_projects_owner_path", "uk_workspace_owner_run", "uk_workspace_repo_path", "uk_workspace_repo_branch"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND index_name = ?", index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("required migration index %s is missing", index)
		}
	}
	for _, column := range []string{"repository_root_hash", "workspace_path_hash", "worktree_path_hash", "worktree_branch_name"} {
		var extra string
		if err := db.QueryRow("SELECT extra FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'agent_workspaces' AND column_name = ?", column).Scan(&extra); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToUpper(extra), "STORED GENERATED") {
			t.Fatalf("agent_workspaces.%s is not stored generated: %q", column, extra)
		}
	}
}
