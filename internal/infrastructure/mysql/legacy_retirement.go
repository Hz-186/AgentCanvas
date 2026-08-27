package mysql

import (
	"context"
	"fmt"

	"agentcanvas/internal/domain/contextresource"

	"gorm.io/gorm"
)

// LegacySchemaRetirement is the SQL-side of the Task 8 cleanup stage. Every
// statement is DROP TABLE IF EXISTS so the actions are rerun-safe, and none of
// them may run unless the caller's migration and ES backfill validations have
// already passed (enforced by memory_usecase.LegacyCleanup, which owns the
// ordering). The same DDL is recorded as migration 000017 with a valid .down.
type LegacySchemaRetirement struct{ db *gorm.DB }

func NewLegacySchemaRetirement(db *gorm.DB) *LegacySchemaRetirement {
	return &LegacySchemaRetirement{db: db}
}

var legacyReflectionTables = []string{
	"agent_reflections",
	"agent_reflection_recall_logs",
	"agent_reflection_jobs",
	"agent_reflection_job_outbox",
	"agent_reflection_evidence",
}

// DropReflectionTables drops the retired reflection tables. ZERO drops happen
// when any statement fails before it: the failure is returned and the
// remaining statements are not attempted, so a failed run stays rerunnable.
func (r *LegacySchemaRetirement) DropReflectionTables(ctx context.Context) error {
	return r.dropTables(ctx, legacyReflectionTables)
}

// DropMemoryWriteLogs drops the retired memory_write_logs table.
func (r *LegacySchemaRetirement) DropMemoryWriteLogs(ctx context.Context) error {
	return r.dropTables(ctx, []string{"memory_write_logs"})
}

// DropRetiredSurfaces drops the retired reflection tables and
// memory_write_logs in one pass.
func (r *LegacySchemaRetirement) DropRetiredSurfaces(ctx context.Context) error {
	if err := r.DropReflectionTables(ctx); err != nil {
		return err
	}
	return r.DropMemoryWriteLogs(ctx)
}

func (r *LegacySchemaRetirement) dropTables(ctx context.Context, tables []string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("legacy schema retirement is not configured")
	}
	for _, table := range tables {
		stmt := fmt.Sprintf("DROP TABLE IF EXISTS `%s`", table)
		if err := r.db.WithContext(ctx).Exec(stmt).Error; err != nil {
			return fmt.Errorf("drop retired table %s: %w", table, err)
		}
	}
	return nil
}

// ValidateContextBackfill confirms the unified context keyword index has been
// backfilled for long-term memories before any destructive cleanup runs. It
// is intentionally strict: while the memories table holds rows and no context
// outbox completion can be confirmed, cleanup stays blocked. The caller is
// expected to run cmd/backfill-context-index first; this check is the
// deployment gate, not the backfill itself.
func (r *LegacySchemaRetirement) ValidateContextBackfill(ctx context.Context) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("legacy schema retirement is not configured")
	}
	var pending int64
	// Pending or processing context outbox rows for long-term memories mean
	// the keyword index is not yet converged: cleanup must not proceed.
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM context_resource_index_outbox WHERE resource_type = ? AND status IN (?, ?)`,
		contextresource.TypeLongTermMemory, contextresource.StatusPending, contextresource.StatusProcessing,
	).Scan(&pending).Error; err != nil {
		return fmt.Errorf("check context backfill state: %w", err)
	}
	if pending > 0 {
		return fmt.Errorf("context keyword backfill is incomplete: %d long_term_memory resource(s) pending", pending)
	}
	return nil
}
