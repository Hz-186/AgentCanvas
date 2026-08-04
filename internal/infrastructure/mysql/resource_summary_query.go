package mysql

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"agentcanvas/internal/domain/resource"
	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type ResourceSummaryQuery struct {
	db *gorm.DB
}

type resourceCursor struct {
	Version   int       `json:"v"`
	ID        int64     `json:"id"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func NewResourceSummaryQuery(db *gorm.DB) *ResourceSummaryQuery {
	return &ResourceSummaryQuery{db: db}
}

func (q *ResourceSummaryQuery) List(ctx context.Context, ownerID int64, kind resource.Kind, options resource.ListOptions) (resource.Page, error) {
	if ownerID <= 0 || !kind.Valid() {
		return resource.Page{}, agenterrors.ErrInvalidInput
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	cursor, err := decodeResourceCursor(options.Cursor)
	if err != nil {
		return resource.Page{}, agenterrors.ErrInvalidInput
	}

	query, updatedOrder, err := q.queryForKind(ctx, ownerID, kind)
	if err != nil {
		return resource.Page{}, err
	}
	if cursor != nil {
		if updatedOrder {
			query = query.Where("updated_at < ? OR (updated_at = ? AND id < ?)", cursor.UpdatedAt, cursor.UpdatedAt, cursor.ID)
		} else {
			query = query.Where("id < ?", cursor.ID)
		}
	}
	if updatedOrder {
		query = query.Order("updated_at DESC").Order("id DESC")
	} else {
		query = query.Order("id DESC")
	}

	items := make([]resource.Summary, 0, limit+1)
	if err := query.Limit(limit + 1).Scan(&items).Error; err != nil {
		return resource.Page{}, err
	}
	page := resource.Page{Items: items}
	if len(items) > limit {
		page.HasMore = true
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = encodeResourceCursor(resourceCursor{Version: 1, ID: last.ID, UpdatedAt: last.UpdatedAt})
		if err != nil {
			return resource.Page{}, err
		}
	}
	return page, nil
}

func (q *ResourceSummaryQuery) queryForKind(ctx context.Context, ownerID int64, kind resource.Kind) (*gorm.DB, bool, error) {
	base := q.db.WithContext(ctx).Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	switch kind {
	case resource.KindSkills:
		return base.Table("skills").Select("id, name, description, status, skill_type AS resource_type, updated_at"), true, nil
	case resource.KindMemories:
		return base.Table("memories").Select("id, COALESCE(NULLIF(title, ''), memory_type) AS name, 1 AS status, memory_type AS resource_type, updated_at"), true, nil
	case resource.KindHTTPTools:
		return base.Table("tool_definitions").Where("tool_type = ?", "http").Select("id, name, description, status, tool_type AS resource_type, updated_at"), true, nil
	case resource.KindKnowledgeBases:
		return base.Table("knowledge_bases").Select("id, name, description, status, updated_at, document_count, chunk_count"), false, nil
	default:
		return nil, false, fmt.Errorf("unsupported resource kind %q", kind)
	}
}

func decodeResourceCursor(value string) (*resourceCursor, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(data) > 512 {
		return nil, agenterrors.ErrInvalidInput
	}
	var cursor resourceCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.ID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	return &cursor, nil
}

func encodeResourceCursor(cursor resourceCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
