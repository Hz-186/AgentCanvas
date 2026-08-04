package mysql

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	agentdomain "agentcanvas/internal/domain/agent"

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
		"agent_runs": {"id", "owner_id", "agent_id", "agent_release_id", "conversation_id", "parent_run_id", "run_type", "delegation_depth", "definition_json", "definition_hash", "rule_hash", "status", "input_json", "output_json", "error_message", "total_tokens", "latency_ms", "started_at", "finished_at", "created_at", "updated_at"},
		"agent_run_events": {"id", "owner_id", "run_id", "event_type", "payload_json", "created_at"},
		"agent_run_steps": {"id", "owner_id", "run_id", "step_index", "step_type", "role", "content", "tool_call_id", "tool_name", "arguments_json", "output_json", "compressed", "error_message", "token_count", "latency_ms", "provider_id", "model", "created_at"},
		"agent_run_checkpoints": {"id", "owner_id", "run_id", "status", "snapshot_version", "interaction_id", "runtime_checkpoint_json", "messages_json", "messages_summary", "steps_json", "pending_tool_call_json", "context_json", "tool_registry_hash", "tool_policy_hash", "created_at", "updated_at"},
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
		if !reflect.DeepEqual(actual, expected) {
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
