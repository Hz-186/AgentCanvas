package mysql

import (
	"context"
	"encoding/json"
	"time"

	"agentcanvas/internal/domain/dialog"
	"agentcanvas/internal/domain/knowledge"

	"gorm.io/gorm"
)

type DialogRepository struct {
	db *gorm.DB
}

func NewDialogRepository(db *gorm.DB) *DialogRepository {
	return &DialogRepository{db: db}
}

func (r *DialogRepository) Create(ctx context.Context, item *dialog.Dialog) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	normalizeDialog(item)
	if err := r.db.WithContext(ctx).Create(item).Error; err != nil {
		return err
	}
	hydrateDialog(item)
	return nil
}

func (r *DialogRepository) ListByOwner(ctx context.Context, ownerID int64) ([]dialog.Dialog, error) {
	var items []dialog.Dialog
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND deleted_at IS NULL", ownerID).
		Order("updated_at DESC, id DESC").
		Find(&items).Error
	for i := range items {
		hydrateDialog(&items[i])
	}
	return items, err
}

func (r *DialogRepository) FindByID(ctx context.Context, ownerID, id int64) (*dialog.Dialog, error) {
	var item dialog.Dialog
	err := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	hydrateDialog(&item)
	return &item, nil
}

func (r *DialogRepository) Update(ctx context.Context, item *dialog.Dialog) error {
	item.UpdatedAt = time.Now().UTC()
	normalizeDialog(item)
	if err := r.db.WithContext(ctx).Save(item).Error; err != nil {
		return err
	}
	hydrateDialog(item)
	return nil
}

func (r *DialogRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&dialog.Dialog{}).
		Where("id = ? AND owner_id = ? AND deleted_at IS NULL", id, ownerID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func normalizeDialog(item *dialog.Dialog) {
	if item.Status == 0 {
		item.Status = dialog.StatusActive
	}
	if item.TopK <= 0 {
		item.TopK = 8
	}
	if item.RetrievalMode == "" {
		item.RetrievalMode = knowledge.RetrievalModeKeyword
	}
	if item.HistoryRoundLimit <= 0 {
		item.HistoryRoundLimit = 8
	}
	if item.Prologue == "" {
		item.Prologue = "你好，我可以帮你什么？"
	}
	raw, _ := json.Marshal(item.KBIDs)
	item.KBIDsJSON = string(raw)
}

func hydrateDialog(item *dialog.Dialog) {
	if item == nil {
		return
	}
	if item.KBIDsJSON == "" {
		item.KBIDs = []int64{}
		return
	}
	if err := json.Unmarshal([]byte(item.KBIDsJSON), &item.KBIDs); err != nil {
		item.KBIDs = []int64{}
	}
}
