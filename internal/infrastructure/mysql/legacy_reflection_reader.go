package mysql

import (
	"context"

	"agentcanvas/internal/domain/reflection"

	"gorm.io/gorm"
)

// LegacyReflectionReader is the migration-only read seam over the
// agent_reflections table. It exists solely so historical rows can be
// converted into ordinary memories before 000017 drops the table. No
// production path may use it after conversion.
type LegacyReflectionReader struct{ db *gorm.DB }

func NewLegacyReflectionReader(db *gorm.DB) *LegacyReflectionReader {
	return &LegacyReflectionReader{db: db}
}

// ListHistorical returns non-deleted reflection rows for one owner in the
// given statuses, ordered by id ascending. The statuses filter is explicit so
// candidate rows that never validated are excluded from conversion.
func (r *LegacyReflectionReader) ListHistorical(ctx context.Context, ownerID int64, statuses []string) ([]reflection.Reflection, error) {
	var rows []reflection.Reflection
	query := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListHistoricalOwnerIDs returns the distinct owners that still hold
// historical reflection rows. It drives the per-owner conversion sweep.
func (r *LegacyReflectionReader) ListHistoricalOwnerIDs(ctx context.Context, statuses []string) ([]int64, error) {
	var owners []int64
	query := r.db.WithContext(ctx).Model(&reflection.Reflection{}).Distinct("owner_id")
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	if err := query.Order("owner_id ASC").Pluck("owner_id", &owners).Error; err != nil {
		return nil, err
	}
	return owners, nil
}
