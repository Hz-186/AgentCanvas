package mysql

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/memory"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMemoryIndexScopeDoesNotBindProjectMemoryToSourceConversation(t *testing.T) {
	conversationID, projectID := int64(7), int64(42)
	item := &memory.Memory{SourceConversationID: &conversationID, SourceProjectID: &projectID, ScopeType: memory.ScopeProject, ScopeID: projectID}
	_, indexedConversationID, indexedProjectID := memoryIndexScope(item)
	if indexedConversationID != 0 || indexedProjectID != projectID {
		t.Fatalf("project memory index scope = conversation:%d project:%d", indexedConversationID, indexedProjectID)
	}
	item.ScopeType, item.ScopeID = memory.ScopeUser, 1
	indexedAgentID, indexedConversationID, indexedProjectID := memoryIndexScope(item)
	if indexedAgentID != 0 || indexedConversationID != 0 || indexedProjectID != 0 {
		t.Fatalf("user memory index scope used source fields: agent:%d conversation:%d project:%d", indexedAgentID, indexedConversationID, indexedProjectID)
	}
}

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
	old := &memory.Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}}, MemoryType: memory.TypeProfile, RetentionTier: memory.TierLongTerm,
		Title: "response style", Content: "User prefers concise answers", Importance: 1, Source: "manual",
		DeduplicationKey: &oldSourceKey, ScopeType: memory.ScopeAgent, ScopeID: 7, Status: memory.StatusSuperseded}
	if err := repository.Create(ctx, old); err != nil {
		t.Fatal(err)
	}

	replacementSourceKey := "memory-v2-integration:replacement"
	replacement := &memory.Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}}, MemoryType: memory.TypeProfile, RetentionTier: memory.TierLongTerm,
		Title: "response style", Content: "User prefers detailed answers", Importance: 1, Source: "proposal",
		DeduplicationKey: &replacementSourceKey, ScopeType: memory.ScopeAgent, ScopeID: 7, Status: memory.StatusActive, SupersedesID: &old.ID}
	if err := repository.Create(ctx, replacement); err != nil {
		t.Fatal(err)
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
	if err != nil || used.UsageCount != 1 || used.LastUsedAt == nil {
		t.Fatalf("actual recall must atomically update lifecycle counters: memory=%+v err=%v", used, err)
	}

	details, err := json.Marshal([]memory.RecallDetail{{MemoryID: replacement.ID, Source: replacement.Source, ScopeType: replacement.ScopeType, ScopeID: replacement.ScopeID, Score: 0.9, Reason: "unified_context_index", TokenCost: 8}})
	if err != nil {
		t.Fatal(err)
	}
	recallLogs := NewMemoryRecallLogRepository(db)
	logItem := &memory.RecallLog{ImmutableModel: domain.ImmutableModel{OwnerID: ownerID}, AgentID: 7, RunID: 11, Query: "response style", CandidateJSON: json.RawMessage(`{"score":0.9}`), InjectedJSON: details, TokenCost: 8}
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
	if outboxCount < 2 {
		t.Fatalf("expected one outbox row per created memory, got %d", outboxCount)
	}
}

func pointerInt64(value int64) *int64 { return &value }
