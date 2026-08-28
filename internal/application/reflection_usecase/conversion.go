package reflection_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
)

// HistoricalReflectionReader lists legacy reflection rows for conversion. It
// is a migration-only seam: the agent_reflections table is dropped by the
// retirement migration once conversion has landed in memories.
type HistoricalReflectionReader interface {
	ListHistorical(ctx context.Context, ownerID int64, statuses []string) ([]reflection.Reflection, error)
	ListHistoricalOwnerIDs(ctx context.Context, statuses []string) ([]int64, error)
}

// ReflectionMemorySink is the canonical memory write seam for converted
// reflections. The SQL MemoryRepository.Create upsert on
// (owner_id, deduplication_key) makes repeated conversion runs idempotent.
type ReflectionMemorySink interface {
	Create(ctx context.Context, item *memory.Memory) error
}

// ReflectionMigration converts historical reflection rows into ordinary
// memories. Only rows that reached a durable state (validated, disputed,
// superseded, archived) are migrated; candidates that never validated carry
// no durable value.
type ReflectionMigration struct {
	Reader HistoricalReflectionReader
	Sink   ReflectionMemorySink
}

var historicalReflectionStatuses = []string{
	reflection.StatusValidated,
	reflection.StatusDisputed,
	reflection.StatusSuperseded,
	reflection.StatusArchived,
}

// HistoricalOwnerIDs returns the distinct owners that still hold convertible
// reflection rows, sorted ascending.
func (m *ReflectionMigration) HistoricalOwnerIDs(ctx context.Context) ([]int64, error) {
	if m == nil || m.Reader == nil {
		return nil, fmt.Errorf("reflection migration is not configured")
	}
	return m.Reader.ListHistoricalOwnerIDs(ctx, historicalReflectionStatuses)
}

func (m *ReflectionMigration) Run(ctx context.Context, ownerID int64) error {
	if m == nil || m.Reader == nil || m.Sink == nil {
		return fmt.Errorf("reflection migration is not configured")
	}
	rows, err := m.Reader.ListHistorical(ctx, ownerID, historicalReflectionStatuses)
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for i := range rows {
		item := ConvertHistoricalReflection(rows[i])
		if err := m.Sink.Create(ctx, &item); err != nil {
			return fmt.Errorf("convert reflection %d: %w", rows[i].ID, err)
		}
	}
	return nil
}

// ConvertHistoricalReflection maps one legacy reflection row to an ordinary
// agent-scoped task memory. Validated (and candidate/active) rows stay
// active, disputed and archived rows become revoked, superseded stays
// superseded. Provenance, root cause, evidence, tags and usage counters are
// preserved in MetadataJSON; the deduplication key reflection:<id> keeps the
// conversion idempotent across reruns.
func ConvertHistoricalReflection(row reflection.Reflection) memory.Memory {
	dedupKey := fmt.Sprintf("reflection:%d", row.ID)
	status := memory.StatusActive
	switch row.Status {
	case reflection.StatusDisputed, reflection.StatusArchived:
		status = memory.StatusRevoked
	case reflection.StatusSuperseded:
		status = memory.StatusSuperseded
	}
	importance := row.Importance
	if importance <= 0 {
		importance = 0.5
	}
	if importance > 1 {
		importance = 1
	}
	title := strings.TrimSpace(row.TaskSummary)
	if title == "" {
		title = "Reflection lesson"
	}
	content := strings.TrimSpace(row.Lesson)
	if corrective := strings.TrimSpace(row.CorrectiveAction); corrective != "" {
		content += "\n\nCorrective action: " + corrective
	}
	metadata := map[string]any{
		"reflection_id":        row.ID,
		"reflection_status":    row.Status,
		"root_cause_category":  row.RootCauseCategory,
		"root_cause":           row.RootCause,
		"recall_count":         row.RecallCount,
		"successful_use_count": row.SuccessfulUseCount,
		"harmful_count":        row.HarmfulCount,
	}
	if len(row.EvidenceJSON) > 0 {
		metadata["evidence"] = row.EvidenceJSON
	}
	if len(row.TagsJSON) > 0 {
		metadata["tags"] = row.TagsJSON
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		metadataJSON = nil
	}
	return memory.Memory{
		SoftDeleteModel:  domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: row.OwnerID}},
		ScopeType:        memory.ScopeAgent,
		ScopeID:          row.AgentID,
		Status:           status,
		MemoryType:       memory.TypeTask,
		RetentionTier:    memory.TierLongTerm,
		Title:            title,
		Content:          content,
		Importance:       importance,
		Source:           "reflection",
		DeduplicationKey: &dedupKey,
		MetadataJSON:     metadataJSON,
	}
}
