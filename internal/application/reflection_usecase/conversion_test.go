package reflection_usecase

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
)

type fakeHistoricalReflectionReader struct {
	rows          []reflection.Reflection
	ownerIDs      []int64
	sweepOwnerIDs []int64
	statuses      []string
}

func (f *fakeHistoricalReflectionReader) ListHistorical(_ context.Context, ownerID int64, statuses []string) ([]reflection.Reflection, error) {
	f.ownerIDs = append(f.ownerIDs, ownerID)
	f.statuses = append([]string(nil), statuses...)
	return f.rows, nil
}

func (f *fakeHistoricalReflectionReader) ListHistoricalOwnerIDs(_ context.Context, statuses []string) ([]int64, error) {
	f.statuses = append([]string(nil), statuses...)
	return f.sweepOwnerIDs, nil
}

type fakeReflectionMemorySink struct {
	created []memory.Memory
}

func (f *fakeReflectionMemorySink) Create(_ context.Context, item *memory.Memory) error {
	f.created = append(f.created, *item)
	return nil
}

func softDeleteModelFor(id, ownerID int64) domain.SoftDeleteModel {
	return domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: id, OwnerID: ownerID}}
}

// metadataJSON marshals one metadata entry back to JSON so raw nested values
// (evidence, tags) can be compared without type guessing.
func metadataJSON(t *testing.T, metadata map[string]any, key string) string {
	t.Helper()
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal metadata %s: %v", key, err)
	}
	return string(raw)
}

func TestReflectionMigrationShouldConvertHistoricalRows(t *testing.T) {
	reader := &fakeHistoricalReflectionReader{rows: []reflection.Reflection{
		{
			SoftDeleteModel: softDeleteModelFor(11, 42),
			AgentID:         5, Scope: reflection.ScopeAgent, Kind: reflection.KindImportantStrategy,
			Status: reflection.StatusValidated, TaskSummary: "summary A", Lesson: "lesson A",
			CorrectiveAction: "act A", RootCauseCategory: "category A", RootCause: "root A",
			EvidenceJSON: json.RawMessage(`{"runs":[1]}`), TagsJSON: json.RawMessage(`["tag-a"]`),
			Importance: 0.8, RecallCount: 3, SuccessfulUseCount: 2, HarmfulCount: 0,
		},
		{
			SoftDeleteModel: softDeleteModelFor(12, 42),
			AgentID:         5, Scope: reflection.ScopeAgent, Kind: reflection.KindErrorLesson,
			Status: reflection.StatusDisputed, TaskSummary: "summary B", Lesson: "lesson B",
			RootCause: "root B", Importance: 0.6, RecallCount: 1,
		},
		{
			SoftDeleteModel: softDeleteModelFor(13, 42),
			AgentID:         6, Scope: reflection.ScopeAgent, Kind: reflection.KindImportantStrategy,
			Status: reflection.StatusSuperseded, TaskSummary: "summary C", Lesson: "lesson C",
			RootCause: "root C", Importance: 0.7,
		},
		{
			SoftDeleteModel: softDeleteModelFor(14, 42),
			AgentID:         5, Scope: reflection.ScopeAgent, Kind: reflection.KindErrorLesson,
			Status: reflection.StatusArchived, TaskSummary: "summary D", Lesson: "lesson D",
			RootCause: "root D", Importance: 0.5,
		},
	}}
	sink := &fakeReflectionMemorySink{}
	migration := &ReflectionMigration{Reader: reader, Sink: sink}

	if err := migration.Run(context.Background(), 42); err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	if len(reader.ownerIDs) != 1 || reader.ownerIDs[0] != 42 {
		t.Fatalf("reader owner ids = %v, want [42]", reader.ownerIDs)
	}
	for _, status := range []string{reflection.StatusValidated, reflection.StatusDisputed, reflection.StatusSuperseded, reflection.StatusArchived} {
		found := false
		for _, requested := range reader.statuses {
			if requested == status {
				found = true
			}
		}
		if !found {
			t.Fatalf("reader statuses %v do not include %s", reader.statuses, status)
		}
	}

	if len(sink.created) != 4 {
		t.Fatalf("converted memory count = %d, want 4", len(sink.created))
	}
	byKey := map[string]memory.Memory{}
	for _, item := range sink.created {
		if item.DeduplicationKey == nil {
			t.Fatalf("converted memory %d has no deduplication key", item.ID)
		}
		byKey[*item.DeduplicationKey] = item
	}

	wantStatus := map[string]string{
		"reflection:11": memory.StatusActive,
		"reflection:12": memory.StatusRevoked,
		"reflection:13": memory.StatusSuperseded,
		"reflection:14": memory.StatusRevoked,
	}
	wantAgent := map[string]int64{"reflection:11": 5, "reflection:12": 5, "reflection:13": 6, "reflection:14": 5}
	wantReflectionStatus := map[string]string{
		"reflection:11": reflection.StatusValidated,
		"reflection:12": reflection.StatusDisputed,
		"reflection:13": reflection.StatusSuperseded,
		"reflection:14": reflection.StatusArchived,
	}
	lessons := map[string]string{"reflection:11": "lesson A", "reflection:12": "lesson B", "reflection:13": "lesson C", "reflection:14": "lesson D"}
	for key, want := range wantStatus {
		item, ok := byKey[key]
		if !ok {
			t.Fatalf("missing converted memory for %s", key)
		}
		if item.OwnerID != 42 {
			t.Fatalf("%s owner = %d, want 42", key, item.OwnerID)
		}
		if item.Source != "reflection" {
			t.Fatalf("%s source = %q, want reflection", key, item.Source)
		}
		if item.Status != want {
			t.Fatalf("%s status = %q, want %q", key, item.Status, want)
		}
		if item.MemoryType != memory.TypeTask {
			t.Fatalf("%s memory type = %q, want %q", key, item.MemoryType, memory.TypeTask)
		}
		if item.ScopeType != memory.ScopeAgent || item.ScopeID != wantAgent[key] {
			t.Fatalf("%s scope = %s/%d, want %s/%d", key, item.ScopeType, item.ScopeID, memory.ScopeAgent, wantAgent[key])
		}
		if item.Content == "" || !strings.Contains(item.Content, lessons[key]) {
			t.Fatalf("%s content %q does not contain lesson %q", key, item.Content, lessons[key])
		}
		var metadata map[string]any
		if len(item.MetadataJSON) == 0 {
			t.Fatalf("%s metadata is empty", key)
		}
		if err := json.Unmarshal(item.MetadataJSON, &metadata); err != nil {
			t.Fatalf("%s metadata %q: %v", key, item.MetadataJSON, err)
		}
		if metadata["reflection_status"] != wantReflectionStatus[key] {
			t.Fatalf("%s metadata reflection_status = %v, want %s", key, metadata["reflection_status"], wantReflectionStatus[key])
		}
	}
	first := byKey["reflection:11"]
	var metadata map[string]any
	if err := json.Unmarshal(first.MetadataJSON, &metadata); err != nil {
		t.Fatalf("first metadata %q: %v", first.MetadataJSON, err)
	}
	if metadata["root_cause"] != "root A" {
		t.Fatalf("metadata root_cause = %v, want root A", metadata["root_cause"])
	}
	if metadata["root_cause_category"] != "category A" {
		t.Fatalf("metadata root_cause_category = %v, want category A", metadata["root_cause_category"])
	}
	if metadataJSON(t, metadata, "tags") != `["tag-a"]` {
		t.Fatalf("metadata tags = %s, want [\"tag-a\"]", metadataJSON(t, metadata, "tags"))
	}
	if metadataJSON(t, metadata, "evidence") != `{"runs":[1]}` {
		t.Fatalf("metadata evidence = %s, want {\"runs\":[1]}", metadataJSON(t, metadata, "evidence"))
	}
	if metadata["recall_count"] != float64(3) || metadata["successful_use_count"] != float64(2) || metadata["harmful_count"] != float64(0) {
		t.Fatalf("metadata counts = %v/%v/%v, want 3/2/0", metadata["recall_count"], metadata["successful_use_count"], metadata["harmful_count"])
	}

	// Deterministic order: conversions must be applied in reflection id order.
	var keys []string
	for _, item := range sink.created {
		keys = append(keys, *item.DeduplicationKey)
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("conversion order is not deterministic: %v", keys)
	}
}
