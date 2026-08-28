package mysql

import (
	"agentcanvas/internal/domain"
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
	conversationdomain "agentcanvas/internal/domain/conversation"
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
		"agent_runs":              {"id", "owner_id", "agent_id", "conversation_id", "workspace_id", "parent_run_id", "run_type", "delegation_depth", "definition_json", "definition_hash", "rule_hash", "status", "input_json", "output_json", "error_message", "total_tokens", "latency_ms", "started_at", "finished_at", "created_at", "updated_at"},
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
	run := &agentdomain.Run{BaseModel: domain.BaseModel{OwnerID: ownerID}, AgentID: 7, RunType: agentdomain.RunTypeTurn, Status: agentdomain.RunStatusQueued,
		DefinitionJSON: []byte(`{"mode":"react"}`), DefinitionHash: "definition-hash", RuleHash: "rule-hash",
		InputJSON: []byte(`{"query":"integration"}`), StartedAt: now}
	if err := runRepo.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := eventRepo.Create(context.Background(), &agentdomain.RunEvent{ImmutableModel: domain.ImmutableModel{OwnerID: ownerID}, RunID: run.ID, EventType: "run.queued", PayloadJSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := stepRepo.Create(context.Background(), &agentdomain.RunStep{ImmutableModel: domain.ImmutableModel{OwnerID: ownerID}, RunID: run.ID, StepIndex: 1, StepType: "llm_response", ProviderID: 3, Model: "mock", TokenCount: 4, LatencyMS: 8}); err != nil {
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

func TestAgentTurnClaimLeaseRecoveryIntegration(t *testing.T) {
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
		_ = db.Exec("DELETE FROM agent_turns WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM agent_runs WHERE owner_id = ?", ownerID).Error
	}
	cleanup()
	t.Cleanup(cleanup)

	runRepo := NewRunRepository(db)
	turnRepo := NewAgentTurnRepository(db)
	now := time.Now().UTC()
	run := &agentdomain.Run{BaseModel: domain.BaseModel{OwnerID: ownerID}, AgentID: 1, RunType: agentdomain.RunTypeTurn,
		Status: agentdomain.RunStatusQueued, DefinitionJSON: []byte(`{}`), InputJSON: []byte(`{}`), StartedAt: now}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{OwnerID: ownerID}, AgentID: 1, ConversationID: ownerID,
		RunID: &run.ID, UserMessageID: 1, IdempotencyKey: "claim-once", Status: agentdomain.TurnStatusQueued, InputJSON: []byte(`{}`)}
	if err := turnRepo.Create(ctx, turn); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		turn *agentdomain.Turn
		err  error
	}
	results := make(chan claimResult, 2)
	start := make(chan struct{})
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			<-start
			claimed, claimErr := turnRepo.ClaimNext(ctx, fmt.Sprintf("worker-%d", index), fmt.Sprintf("lease-%d", index), now.Add(time.Minute))
			results <- claimResult{turn: claimed, err: claimErr}
		}()
	}
	close(start)
	var claimed *agentdomain.Turn
	noTurn := 0
	for index := 0; index < 2; index++ {
		result := <-results
		if result.err == nil {
			if claimed != nil {
				t.Fatalf("turn was claimed twice: first=%+v second=%+v", claimed, result.turn)
			}
			claimed = result.turn
		} else if errors.Is(result.err, agentdomain.ErrNoTurnAvailable) {
			noTurn++
		} else {
			t.Fatal(result.err)
		}
	}
	if claimed == nil || noTurn != 1 || claimed.AttemptCount != 1 || claimed.LeaseToken == "" {
		t.Fatalf("unexpected concurrent claim result: claimed=%+v no_turn=%d", claimed, noTurn)
	}

	if err := turnRepo.RenewLease(ctx, claimed.ID, claimed.LeaseToken, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("renew owned lease: %v", err)
	}
	if err := turnRepo.RenewLease(ctx, claimed.ID, "stale-lease", now.Add(3*time.Minute)); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale heartbeat error = %v, want record not found", err)
	}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&agentdomain.Turn{}).Where("id = ?", claimed.ID).Update("lease_expires_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&agentdomain.Run{}).Where("id = ?", run.ID).Update("status", agentdomain.RunStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	expired, err := turnRepo.ListExpiredRunning(ctx, time.Now().UTC(), 10)
	if err != nil || len(expired) != 1 || expired[0].ID != claimed.ID {
		t.Fatalf("expired turns=%+v error=%v", expired, err)
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	claimed.Status, claimed.RetryAt, claimed.LeaseExpiresAt = agentdomain.TurnStatusRetryWait, &retryAt, &expiredAt
	run.Status = agentdomain.RunStatusQueued
	if err := turnRepo.RecoverExpired(ctx, claimed, run); err != nil {
		t.Fatalf("recover expired turn: %v", err)
	}
	stored, err := turnRepo.FindByID(ctx, ownerID, claimed.ID)
	if err != nil || stored.Status != agentdomain.TurnStatusRetryWait || stored.LeaseToken != "" || stored.RetryAt == nil {
		t.Fatalf("recovered turn=%+v error=%v", stored, err)
	}
	storedRun, err := runRepo.FindByID(ctx, ownerID, run.ID)
	if err != nil || storedRun.Status != agentdomain.RunStatusQueued {
		t.Fatalf("recovered run=%+v error=%v", storedRun, err)
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
	projectItem := &projectdomain.Project{BaseModel: domain.BaseModel{OwnerID: ownerID}, Slug: "atomic-project", Name: "Atomic Project", RepositoryRoot: "/tmp/atomic-project"}
	primaryFolder := &projectdomain.ProjectFolder{Label: "Primary"}
	if err := projects.CreateWithPrimaryFolder(ctx, projectItem, primaryFolder); err != nil {
		t.Fatal(err)
	}
	loaded, err := projects.FindByID(ctx, ownerID, projectItem.ID)
	if err != nil || len(loaded.Folders) != 1 || countRepositoryRoots(loaded.Folders) != 1 || !loaded.Folders[0].IsRepositoryRoot || loaded.Folders[0].Path != loaded.RepositoryRoot || loaded.Folders[0].Path != projectItem.RepositoryRoot {
		t.Fatalf("atomic project creation mismatch: project=%#v err=%v", loaded, err)
	}
	secondaryFolder := &projectdomain.ProjectFolder{OwnerID: ownerID, ProjectID: projectItem.ID, Path: "/tmp/atomic-project-secondary", Label: "Secondary"}
	if err := projects.AddFolder(ctx, secondaryFolder); err != nil {
		t.Fatal(err)
	}
	switchFolder := &projectdomain.ProjectFolder{OwnerID: ownerID, ProjectID: projectItem.ID, Path: "/tmp/atomic-project-switched", Label: "Switched"}
	if err := projects.AddPrimaryFolder(ctx, switchFolder); err != nil {
		t.Fatal(err)
	}
	loaded, err = projects.FindByID(ctx, ownerID, projectItem.ID)
	if err != nil || loaded.RepositoryRoot != switchFolder.Path || len(loaded.Folders) != 3 || countRepositoryRoots(loaded.Folders) != 1 || !loaded.Folders[0].IsRepositoryRoot || loaded.Folders[0].ID != switchFolder.ID || loaded.Folders[0].Path != loaded.RepositoryRoot {
		t.Fatalf("primary folder switch mismatch: project=%#v err=%v", loaded, err)
	}
	intruderFolder := &projectdomain.ProjectFolder{OwnerID: ownerID + 1, ProjectID: projectItem.ID, Path: "/tmp/atomic-project-intruder", Label: "Intruder"}
	if err := projects.AddPrimaryFolder(ctx, intruderFolder); !errors.Is(err, agenterrors.ErrNotFound) {
		t.Fatalf("cross-owner primary folder switch error = %v, want not found", err)
	}
	loaded, err = projects.FindByID(ctx, ownerID, projectItem.ID)
	if err != nil || loaded.RepositoryRoot != switchFolder.Path || len(loaded.Folders) != 3 || countRepositoryRoots(loaded.Folders) != 1 || !loaded.Folders[0].IsRepositoryRoot || loaded.Folders[0].ID != switchFolder.ID || loaded.Folders[0].Path != loaded.RepositoryRoot {
		t.Fatalf("failed primary switch changed the existing primary folder: project=%#v err=%v", loaded, err)
	}

	rolledBack := &projectdomain.Project{BaseModel: domain.BaseModel{OwnerID: ownerID}, Slug: "rolled-back", Name: "Rolled Back", RepositoryRoot: "/tmp/rolled-back"}
	duplicateFolderID := &projectdomain.ProjectFolder{ID: primaryFolder.ID, Label: "duplicate primary key"}
	if err := projects.CreateWithPrimaryFolder(ctx, rolledBack, duplicateFolderID); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("atomic rollback error = %v, want conflict", err)
	}
	var rolledBackCount int64
	if err := db.Model(&projectdomain.Project{}).Where("owner_id = ? AND slug = ?", ownerID, rolledBack.Slug).Count(&rolledBackCount).Error; err != nil || rolledBackCount != 0 {
		t.Fatalf("project survived failed primary folder transaction: count=%d err=%v", rolledBackCount, err)
	}

	secondProject := &projectdomain.Project{BaseModel: domain.BaseModel{OwnerID: ownerID}, Slug: "second-project", Name: "Second", RepositoryRoot: "/tmp/second-project"}
	secondFolder := &projectdomain.ProjectFolder{Label: "Primary"}
	if err := projects.CreateWithPrimaryFolder(ctx, secondProject, secondFolder); err != nil {
		t.Fatal(err)
	}
	secondProject.Slug = projectItem.Slug
	if err := projects.Update(ctx, secondProject); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("project update duplicate error = %v, want conflict", err)
	}

	firstWorkspace := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: ownerID}, ProjectID: projectItem.ID, RunID: ownerID + 1, Kind: workspacedomain.KindWorktree, RepositoryRoot: projectItem.RepositoryRoot, WorkspacePath: projectItem.RepositoryRoot + "/.worktrees/one", BranchName: "atomic/one", Status: workspacedomain.StatusReady}
	secondWorkspace := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: ownerID}, ProjectID: projectItem.ID, RunID: ownerID + 2, Kind: workspacedomain.KindWorktree, RepositoryRoot: projectItem.RepositoryRoot, WorkspacePath: projectItem.RepositoryRoot + "/.worktrees/two", BranchName: "atomic/two", Status: workspacedomain.StatusReady}
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

func countRepositoryRoots(folders []projectdomain.ProjectFolder) int {
	count := 0
	for _, folder := range folders {
		if folder.IsRepositoryRoot {
			count++
		}
	}
	return count
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
	if _, err := testDB.Exec(`INSERT INTO documents (owner_id, kb_id, name, original_filename, object_key) VALUES (1, 1, 'legacy', 'legacy.md', 'raw/legacy.md')`); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`INSERT INTO document_chunks (owner_id, kb_id, document_id, chunk_index, content, content_hash) VALUES (1, 1, 1, 0, 'legacy', 'legacy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`INSERT INTO agent_releases (owner_id, agent_id, version_no, definition_json, checksum, rule_hash, tool_schema_hash, resource_versions_json, created_by) VALUES (1, 1, 1, '{}', 'checksum', 'rule', 'tool', '{}', 1)`); err != nil {
		t.Fatal(err)
	}
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000003_document_generations.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000004_embedding_profiles.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000005_ingestion_retry_at.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000006_project_memory_scope.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000007_dream_project_scope.up.sql"))
	assertAdditiveMigrationState(t, testDB, true)
	assertMemoryProjectMigrationState(t, testDB, true)
	if _, err := testDB.Exec(`INSERT INTO memories (owner_id, scope_type, scope_id, memory_type, memory_level, content, importance, source, created_at, updated_at) VALUES (1, 'project', 42, 'task_memory', 'working', 'project fact', 0.8, 'test', NOW(), NOW())`); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`INSERT INTO memory_extraction_jobs (owner_id, conversation_id, project_id, status, created_at) VALUES (1, 1, 42, 'superseded', NOW())`); err != nil {
		t.Fatal(err)
	}
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000008_model_schema_cleanup.up.sql"))
	assertModelCleanupMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000009_remove_agent_releases.up.sql"))
	assertReleaseRemovalMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000010_conversation_compaction_alignment.up.sql"))
	assertCompactionAlignmentMigrationState(t, testDB, true)
	assertCompactionWindowNumbers(t, testDB)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000010_conversation_compaction_alignment.down.sql"))
	assertCompactionAlignmentMigrationState(t, testDB, false)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000010_conversation_compaction_alignment.up.sql"))
	assertCompactionAlignmentMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000010_conversation_compaction_alignment.down.sql"))
	assertCompactionAlignmentMigrationState(t, testDB, false)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000009_remove_agent_releases.down.sql"))
	assertReleaseRemovalMigrationState(t, testDB, false)
	var memoryType, retentionTier string
	if err := testDB.QueryRow("SELECT memory_type, retention_tier FROM memories WHERE owner_id = 1 LIMIT 1").Scan(&memoryType, &retentionTier); err != nil {
		t.Fatal(err)
	}
	if memoryType != "task" || retentionTier != "short_term" {
		t.Fatalf("memory enum migration = (%q, %q), want (task, short_term)", memoryType, retentionTier)
	}
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000008_model_schema_cleanup.down.sql"))
	assertModelCleanupMigrationState(t, testDB, false)
	if err := testDB.QueryRow("SELECT memory_type, memory_level FROM memories WHERE owner_id = 1 LIMIT 1").Scan(&memoryType, &retentionTier); err != nil {
		t.Fatal(err)
	}
	if memoryType != "task_memory" || retentionTier != "short_term" {
		t.Fatalf("memory enum rollback = (%q, %q), want (task_memory, short_term)", memoryType, retentionTier)
	}
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000007_dream_project_scope.down.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000006_project_memory_scope.down.sql"))
	assertMemoryProjectMigrationState(t, testDB, false)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000005_ingestion_retry_at.down.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000004_embedding_profiles.down.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000003_document_generations.down.sql"))
	assertAdditiveMigrationState(t, testDB, false)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000003_document_generations.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000004_embedding_profiles.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000005_ingestion_retry_at.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000006_project_memory_scope.up.sql"))
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000007_dream_project_scope.up.sql"))
	assertAdditiveMigrationState(t, testDB, true)
	assertMemoryProjectMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000008_model_schema_cleanup.up.sql"))
	assertModelCleanupMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000002_git_workspace.down.sql"))
	assertGitWorkspaceMigrationState(t, testDB, false)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000002_git_workspace.up.sql"))
	assertGitWorkspaceMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000009_remove_agent_releases.up.sql"))
	assertReleaseRemovalMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000010_conversation_compaction_alignment.up.sql"))
	assertCompactionAlignmentMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000016_sql_memory_canonical.up.sql"))
	assertSQLMemoryCanonicalMigrationState(t, testDB, true)
	applyIntegrationMigration(t, testDB, filepath.Join(migrationRoot, "000016_sql_memory_canonical.down.sql"))
	assertSQLMemoryCanonicalMigrationState(t, testDB, false)
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

func assertAdditiveMigrationState(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()
	for table, column := range map[string]string{
		"documents":       "active_generation",
		"document_chunks": "generation",
		"knowledge_bases": "embedding_metric",
		"ingestion_jobs":  "retry_at",
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != expected {
			t.Fatalf("column %s.%s existence = %v, want %v", table, column, count == 1, expected)
		}
	}
	for _, index := range []string{"uk_doc_generation_chunk_index", "idx_document_generation", "idx_ingestion_retry_at"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND index_name = ?", index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != expected {
			t.Fatalf("index %s existence = %v, want %v", index, count == 1, expected)
		}
	}
	if !expected {
		return
	}
	var activeGeneration, generation, metric string
	if err := db.QueryRow("SELECT active_generation FROM documents WHERE id = 1").Scan(&activeGeneration); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT generation FROM document_chunks WHERE document_id = 1 AND chunk_index = 0").Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT column_default FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'knowledge_bases' AND column_name = 'embedding_metric'").Scan(&metric); err != nil {
		t.Fatal(err)
	}
	if activeGeneration != "legacy" || generation != "legacy" || metric != "COSINE" {
		t.Fatalf("unexpected additive migration backfill/defaults: active_generation=%q generation=%q embedding_metric=%q", activeGeneration, generation, metric)
	}
}

func assertMemoryProjectMigrationState(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()
	for table, column := range map[string]string{"memories": "project_id", "memory_extraction_jobs": "project_id"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != expected {
			t.Fatalf("column %s.%s existence = %v, want %v", table, column, count == 1, expected)
		}
	}
	for _, index := range []string{"idx_memories_project", "idx_memory_extraction_project"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND index_name = ?", index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != expected {
			t.Fatalf("index %s existence = %v, want %v", index, count == 1, expected)
		}
	}
	for table, column := range map[string]string{"memories": "scope_type", "memory_extraction_jobs": "status"} {
		var columnType string
		if err := db.QueryRow("SELECT column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&columnType); err != nil {
			t.Fatal(err)
		}
		value := "'project'"
		if table == "memory_extraction_jobs" {
			value = "'superseded'"
		}
		if strings.Contains(columnType, value) != expected {
			t.Fatalf("column %s.%s type = %q, expected extension=%v", table, column, columnType, expected)
		}
	}
}

func assertModelCleanupMigrationState(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()
	for table, columns := range map[string][]string{
		"agent_run_checkpoints":     {"checkpoint_json"},
		"agent_releases":            {"version_number"},
		"knowledge_bases":           {"enabled", "vector_weight"},
		"documents":                 {"knowledge_base_id", "file_size_bytes", "storage_object_key", "active_generation_id", "ingestion_status", "ingestion_error"},
		"document_chunks":           {"knowledge_base_id", "generation_id", "page_number"},
		"memories":                  {"conflict_with_id", "has_conflict", "source_conversation_id", "source_project_id", "retention_tier", "recall_count", "promotion_count", "deduplication_key", "last_recalled_at"},
		"mcp_servers":               {"enabled", "discovery_error", "tools_discovered_at"},
		"mcp_tool_cache":            {"mcp_server_id", "input_schema_json"},
		"skills":                    {"content_markdown", "enabled", "active_name"},
		"projects":                  {"repository_root", "repository_root_hash"},
		"project_folders":           {"is_repository_root"},
		"agent_workspaces":          {"has_unpushed_commits"},
		"cache_invalidation_outbox": {"attempt_count"},
	} {
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
	for table, columns := range map[string][]string{
		"agent_run_checkpoints":     {"runtime_checkpoint_json", "status", "snapshot_version", "interaction_id", "messages_json", "messages_summary", "steps_json", "pending_tool_call_json", "context_json", "tool_registry_hash", "tool_policy_hash", "updated_at"},
		"agent_releases":            {"version_no", "tool_schema_hash", "resource_versions_json", "created_by"},
		"knowledge_bases":           {"status", "hybrid_weight"},
		"documents":                 {"kb_id", "file_size", "object_key", "active_generation", "parser_status", "parser_error"},
		"document_chunks":           {"kb_id", "generation", "page_no", "es_index", "es_doc_id", "updated_at"},
		"memories":                  {"parent_id", "conflict_flag", "conversation_id", "project_id", "memory_level", "access_count", "consolidation_count", "source_key", "last_used_at", "session_id", "embedding"},
		"mcp_servers":               {"status", "last_error", "discovered_at"},
		"mcp_tool_cache":            {"server_id", "parameters_json", "schema_hash", "cached_at", "updated_at"},
		"oauth_accounts":            {"provider_username", "provider_email", "avatar_url", "access_token_encrypted", "refresh_token_encrypted", "scopes", "token_expires_at", "updated_at"},
		"auth_sessions":             {"user_agent", "ip_address"},
		"api_tokens":                {"last_used_at"},
		"conversations":             {"source", "message_json", "reference_json"},
		"messages":                  {"content_type", "metadata_json"},
		"skills":                    {"content_md", "status", "version"},
		"projects":                  {"primary_path", "primary_path_hash", "icon", "color"},
		"project_folders":           {"is_primary"},
		"agent_workspaces":          {"unpushed"},
		"cache_invalidation_outbox": {"attempts"},
	} {
		for _, column := range columns {
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != !expected {
				t.Fatalf("legacy column %s.%s existence = %v, want %v", table, column, count == 1, !expected)
			}
		}
	}
	if !expected {
		for _, column := range []string{"resource_versions_json", "created_by"} {
			var nullable string
			if err := db.QueryRow("SELECT is_nullable FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'agent_releases' AND column_name = ?", column).Scan(&nullable); err != nil {
				t.Fatal(err)
			}
			if nullable != "NO" {
				t.Fatalf("agent_releases.%s nullability = %q, want NO after rollback", column, nullable)
			}
		}
		for _, column := range []string{"cached_at", "updated_at"} {
			var nullable, extra string
			var defaultValue sql.NullString
			if err := db.QueryRow("SELECT is_nullable, column_default, extra FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'mcp_tool_cache' AND column_name = ?", column).Scan(&nullable, &defaultValue, &extra); err != nil {
				t.Fatal(err)
			}
			if nullable != "NO" || defaultValue.Valid || strings.Contains(strings.ToUpper(extra), "ON UPDATE") {
				t.Fatalf("mcp_tool_cache.%s definition = nullable=%q default=%v extra=%q, want NOT NULL without default or ON UPDATE", column, nullable, defaultValue, extra)
			}
		}
	}
	for _, table := range []string{"model_usage_logs", "message_references", "memory_merge_logs", "tool_policies"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != !expected {
			t.Fatalf("removed table %s existence = %v, want %v", table, count == 1, !expected)
		}
	}
	for table, indexes := range map[string][]string{
		"knowledge_bases": {"idx_enabled"},
		"documents":       {"idx_documents_owner_knowledge_base", "idx_documents_ingestion_status", "idx_documents_knowledge_base_enabled"},
		"document_chunks": {"idx_document_chunks_owner_knowledge_base"},
		"memories":        {"idx_memories_retention_tier", "uq_memories_owner_deduplication_key", "idx_memories_source_conversation", "idx_memories_source_project"},
		"mcp_tool_cache":  {"uk_mcp_tool_cache_server_tool", "idx_mcp_tool_cache_server_id"},
		"skills":          {"idx_skills_owner_enabled_updated"},
		"tool_pack_items": {"uk_tool_pack_item", "idx_tool_pack_items_owner_pack"},
	} {
		for _, index := range indexes {
			var count int
			if err := db.QueryRow("SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?", table, index).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != expected {
				t.Fatalf("index %s.%s existence = %v, want %v", table, index, count == 1, expected)
			}
		}
	}
	for table, indexes := range map[string][]string{
		"knowledge_bases": {"idx_status"},
		"documents":       {"idx_owner_kb", "idx_parser_status", "idx_kb_enabled"},
		"document_chunks": {"idx_owner_kb"},
		"memories":        {"idx_memory_level", "uq_memories_owner_source_key", "idx_conversation_id", "idx_memories_project"},
		"mcp_tool_cache":  {"uniq_mcp_tool_cache_server_name", "idx_mcp_tool_cache_server"},
		"skills":          {"idx_skills_owner_status_updated"},
		"tool_pack_items": {"uk_pack_tool", "idx_owner_pack"},
	} {
		for _, index := range indexes {
			var count int
			if err := db.QueryRow("SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?", table, index).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != !expected {
				t.Fatalf("legacy index %s.%s existence = %v, want %v", table, index, count == 1, !expected)
			}
		}
	}
	for column, wantNew := range map[string]bool{"memory_type": expected, "retention_tier": expected, "memory_level": !expected} {
		var columnType string
		if err := db.QueryRow("SELECT column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'memories' AND column_name = ?", column).Scan(&columnType); err != nil {
			if wantNew {
				t.Fatal(err)
			}
			continue
		}
		if column == "memory_type" && strings.Contains(columnType, "enum('profile','episodic','task','archival')") != wantNew {
			t.Fatalf("memories.memory_type type = %q, want new=%v", columnType, wantNew)
		}
		if column == "retention_tier" && strings.Contains(columnType, "enum('short_term','long_term')") != wantNew {
			t.Fatalf("memories.retention_tier type = %q, want new=%v", columnType, wantNew)
		}
		if column == "memory_level" && strings.Contains(columnType, "enum('working','short_term','long_term')") != wantNew {
			t.Fatalf("memories.memory_level type = %q, want old=%v", columnType, wantNew)
		}
	}
}

func assertReleaseRemovalMigrationState(t *testing.T, db *sql.DB, removed bool) {
	t.Helper()
	var releaseTableCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'agent_releases'").Scan(&releaseTableCount); err != nil {
		t.Fatal(err)
	}
	if (releaseTableCount == 0) != removed {
		t.Fatalf("agent_releases removal = %v, want %v", releaseTableCount == 0, removed)
	}
	for table, column := range map[string]string{
		"agent_improvement_reviews": "agent_release_id",
		"agent_turns":               "agent_release_id",
		"agents":                    "current_release_id",
		"agent_runs":                "agent_release_id",
		"conversations":             "agent_release_id",
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 0) != removed {
			t.Fatalf("column %s.%s removal = %v, want %v", table, column, count == 0, removed)
		}
	}
}

func assertCompactionAlignmentMigrationState(t *testing.T, db *sql.DB, applied bool) {
	t.Helper()
	for column, expected := range map[string]bool{
		"window_number":         applied,
		"context_window_tokens": applied,
		"first_message_content": applied,
		"summary_token_limit":   !applied,
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'conversation_compactions' AND column_name = ?", column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != expected {
			t.Fatalf("conversation_compactions.%s existence = %v, want %v", column, count == 1, expected)
		}
	}
	var columnType string
	if err := db.QueryRow("SELECT column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'conversation_compactions' AND column_name = 'trigger_type'").Scan(&columnType); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(columnType, "'runtime'") != applied {
		t.Fatalf("conversation_compactions.trigger_type = %q, runtime expected=%v", columnType, applied)
	}
}

func assertSQLMemoryCanonicalMigrationState(t *testing.T, db *sql.DB, applied bool) {
	t.Helper()
	for _, column := range []struct{ table, column string }{
		{"memories", "usage_count"},
		{"memories", "last_used_at"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", column.table, column.column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != applied {
			t.Fatalf("column %s.%s existence = %v, want %v", column.table, column.column, count == 1, applied)
		}
	}
	for _, column := range []struct{ table, column string }{
		{"memories", "recall_count"},
		{"memories", "last_recalled_at"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", column.table, column.column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) == applied {
			t.Fatalf("legacy column %s.%s existence = %v, want %v", column.table, column.column, count == 1, !applied)
		}
	}
	for _, table := range []string{"memory_artifacts", "memory_write_jobs"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != applied {
			t.Fatalf("table %s existence = %v, want %v", table, count == 1, applied)
		}
	}
}

func assertCompactionWindowNumbers(t *testing.T, db *sql.DB) {
	t.Helper()
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewConversationCompactionRepository(gormDB)
	ctx := context.Background()
	ownerID, conversationID := time.Now().UnixNano(), int64(77)
	defer func() {
		_ = gormDB.Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).Delete(&conversationSnapshotClaim{}).Error
		_ = gormDB.Where("owner_id = ? AND conversation_id = ?", ownerID, conversationID).Delete(&conversationdomain.Compaction{}).Error
	}()
	first := &conversationdomain.Compaction{
		ImmutableModel: domain.ImmutableModel{OwnerID: ownerID}, ConversationID: conversationID,
		SnapshotVersion: 1, SourceFingerprint: strings.Repeat("a", 64), TriggerType: conversationdomain.CompactionTriggerRuntime,
		Status: conversationdomain.CompactionCompleted, Summary: "first", WindowNumber: 99,
	}
	claimed, err := repo.ClaimSnapshot(ctx, ownerID, conversationID, nil, 0, "window-one", time.Now().Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim first compaction: claimed=%v err=%v", claimed, err)
	}
	if err := repo.CompleteSnapshot(ctx, first, nil, 0, "window-one"); err != nil {
		t.Fatal(err)
	}
	parentID := first.ID
	second := &conversationdomain.Compaction{
		ImmutableModel: domain.ImmutableModel{OwnerID: ownerID}, ConversationID: conversationID,
		ParentSnapshotID: &parentID, SnapshotVersion: 2, SourceFingerprint: strings.Repeat("b", 64), TriggerType: conversationdomain.CompactionTriggerAuto,
		Status: conversationdomain.CompactionCompleted, Summary: "second", WindowNumber: 99,
	}
	claimed, err = repo.ClaimSnapshot(ctx, ownerID, conversationID, &parentID, 1, "window-two", time.Now().Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim second compaction: claimed=%v err=%v", claimed, err)
	}
	if err := repo.CompleteSnapshot(ctx, second, &parentID, 1, "window-two"); err != nil {
		t.Fatal(err)
	}
	if first.WindowNumber != 1 || second.WindowNumber != 2 {
		t.Fatalf("transactional window numbers = (%d, %d), want (1, 2)", first.WindowNumber, second.WindowNumber)
	}
}
