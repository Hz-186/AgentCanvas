package mysql

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMemoryV2RepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTCANVAS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set AGENTCANVAS_TEST_MYSQL_DSN to run MySQL integration tests")
	}
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repository := NewMemoryRepository(db)
	ownerID := int64(990027)
	cleanup := func() {
		_ = db.Exec("DELETE FROM memory_recall_logs WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM context_resource_index_outbox WHERE owner_id = ?", ownerID).Error
		_ = db.Exec("DELETE FROM memories WHERE owner_id = ?", ownerID).Error
	}
	cleanup()
	t.Cleanup(cleanup)
	oldSourceKey := "memory-v2-integration:old"
	old := &memory.Memory{OwnerID: ownerID, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Title: "response style", Content: "User prefers concise answers", Importance: 1, Source: "integration_test",
		SourceKey: &oldSourceKey, ScopeType: memory.ScopeAgent, ScopeID: 7, Status: memory.StatusActive}
	if err := repository.Create(ctx, old); err != nil {
		t.Fatal(err)
	}

	replacementSourceKey := "memory-v2-integration:replacement"
	replacement := &memory.Memory{OwnerID: ownerID, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Title: "response style", Content: "User prefers detailed answers", Importance: 1, Source: "approved_memory_proposal",
		SourceKey: &replacementSourceKey, ScopeType: memory.ScopeAgent, ScopeID: 7, Status: memory.StatusActive}
	if err := repository.Replace(ctx, ownerID, old.ID, replacement); err != nil {
		t.Fatal(err)
	}
	previous, err := repository.FindByID(ctx, ownerID, old.ID)
	if err != nil || previous.Status != memory.StatusSuperseded || replacement.SupersedesID == nil || *replacement.SupersedesID != old.ID {
		t.Fatalf("invalid replacement lineage: previous=%+v replacement=%+v err=%v", previous, replacement, err)
	}

	replayed := &memory.Memory{OwnerID: ownerID, MemoryType: memory.TypeProfile, Content: replacement.Content,
		SourceKey: &replacementSourceKey, ScopeType: memory.ScopeAgent, ScopeID: 7}
	if err := repository.Replace(ctx, ownerID, old.ID, replayed); err != nil {
		t.Fatalf("replacement replay must be idempotent: %v", err)
	}
	if replayed.ID != replacement.ID {
		t.Fatalf("replacement replay created a duplicate: first=%d replay=%d", replacement.ID, replayed.ID)
	}

	filtered, err := repository.ListFiltered(ctx, ownerID, memory.ListFilter{Statuses: []string{memory.StatusActive}, ScopeTypes: []string{memory.ScopeAgent}, ScopeID: pointerInt64(7), Limit: 10})
	if err != nil || len(filtered) != 1 || filtered[0].ID != replacement.ID {
		t.Fatalf("database filters did not isolate active agent memory: items=%+v err=%v", filtered, err)
	}
	if _, err := repository.FindByID(ctx, ownerID+1, replacement.ID); err == nil {
		t.Fatal("cross-owner lookup must not return a memory")
	}

	if err := repository.MarkUsed(ctx, ownerID, []int64{replacement.ID}); err != nil {
		t.Fatal(err)
	}
	used, err := repository.FindByID(ctx, ownerID, replacement.ID)
	if err != nil || used.AccessCount != 1 || used.LastUsedAt == nil {
		t.Fatalf("actual recall must atomically update lifecycle counters: memory=%+v err=%v", used, err)
	}

	decayBase := time.Now().UTC().Add(-72 * time.Hour)
	if err := db.Model(&memory.Memory{}).Where("owner_id = ? AND id = ?", ownerID, replacement.ID).
		Updates(map[string]any{"importance": 1.0, "created_at": decayBase, "last_used_at": decayBase, "last_decay_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if count, err := repository.UpdateDecayedImportance(ctx, ownerID, 0.1); err != nil || count != 1 {
		t.Fatalf("expected one incremental decay, count=%d err=%v", count, err)
	}
	decayed, err := repository.FindByID(ctx, ownerID, replacement.ID)
	if err != nil || decayed.Importance >= 1 || decayed.LastDecayAt == nil {
		t.Fatalf("first decay was not persisted: memory=%+v err=%v", decayed, err)
	}
	firstImportance := decayed.Importance
	if count, err := repository.UpdateDecayedImportance(ctx, ownerID, 0.1); err != nil || count != 0 {
		t.Fatalf("immediate retry must not apply cumulative decay, count=%d err=%v", count, err)
	}
	decayedAgain, err := repository.FindByID(ctx, ownerID, replacement.ID)
	if err != nil || math.Abs(decayedAgain.Importance-firstImportance) > 1e-12 {
		t.Fatalf("importance changed on immediate decay retry: before=%f after=%f err=%v", firstImportance, decayedAgain.Importance, err)
	}

	details, err := json.Marshal([]memory.RecallDetail{{MemoryID: replacement.ID, Source: replacement.Source, ScopeType: replacement.ScopeType, ScopeID: replacement.ScopeID, Score: 0.9, Reason: "unified_context_index", TokenCost: 8}})
	if err != nil {
		t.Fatal(err)
	}
	recallLogs := NewMemoryRecallLogRepository(db)
	logItem := &memory.RecallLog{OwnerID: ownerID, AgentID: 7, RunID: 11, Query: "response style", CandidateJSON: json.RawMessage(`{"score":0.9}`), InjectedJSON: details, TokenCost: 8}
	if err := recallLogs.Create(ctx, logItem); err != nil {
		t.Fatal(err)
	}
	logs, err := recallLogs.List(ctx, ownerID, replacement.ID, 10)
	if err != nil || len(logs) != 1 || logs[0].ID != logItem.ID {
		t.Fatalf("recall JSON lookup failed: logs=%+v err=%v", logs, err)
	}

	var outboxCount int64
	if err := db.Table("context_resource_index_outbox").Where("owner_id = ? AND resource_type = ? AND resource_id IN ?", ownerID, contextresource.TypeLongTermMemory,
		[]string{strconv.FormatInt(old.ID, 10), strconv.FormatInt(replacement.ID, 10)}).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount < 3 {
		t.Fatalf("expected create, replacement upsert and superseded delete outbox rows, got %d", outboxCount)
	}
}

func pointerInt64(value int64) *int64 { return &value }
