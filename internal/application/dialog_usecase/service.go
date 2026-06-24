package dialog_usecase

import (
	"context"
	"errors"
	"strings"

	"agentcanvas/internal/domain/dialog"
	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type Service struct {
	dialogs dialog.Repository
}

type CreateDialogRequest struct {
	Name              string  `json:"name" binding:"required"`
	Description       string  `json:"description"`
	ProviderID        int64   `json:"provider_id"`
	Model             string  `json:"model"`
	SystemPrompt      string  `json:"system_prompt"`
	Prologue          string  `json:"prologue"`
	KBIDs             []int64 `json:"kb_ids"`
	TopK              int     `json:"top_k"`
	RetrievalMode     string  `json:"retrieval_mode"`
	HistoryRoundLimit int     `json:"history_round_limit"`
}

type UpdateDialogRequest struct {
	Name              *string  `json:"name"`
	Description       *string  `json:"description"`
	ProviderID        *int64   `json:"provider_id"`
	Model             *string  `json:"model"`
	SystemPrompt      *string  `json:"system_prompt"`
	Prologue          *string  `json:"prologue"`
	KBIDs             *[]int64 `json:"kb_ids"`
	TopK              *int     `json:"top_k"`
	RetrievalMode     *string  `json:"retrieval_mode"`
	HistoryRoundLimit *int     `json:"history_round_limit"`
	Status            *int     `json:"status"`
}

func NewService(dialogs dialog.Repository) *Service {
	return &Service{dialogs: dialogs}
}

func (s *Service) Create(ctx context.Context, ownerID int64, req CreateDialogRequest) (*dialog.Dialog, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || req.ProviderID < 0 || req.TopK < 0 || req.HistoryRoundLimit < 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &dialog.Dialog{
		OwnerID:           ownerID,
		Name:              name,
		Description:       strings.TrimSpace(req.Description),
		ProviderID:        req.ProviderID,
		Model:             strings.TrimSpace(req.Model),
		SystemPrompt:      strings.TrimSpace(req.SystemPrompt),
		Prologue:          strings.TrimSpace(req.Prologue),
		KBIDs:             req.KBIDs,
		TopK:              req.TopK,
		RetrievalMode:     strings.TrimSpace(req.RetrievalMode),
		HistoryRoundLimit: req.HistoryRoundLimit,
		Status:            dialog.StatusActive,
	}
	if err := s.dialogs.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, ownerID int64) ([]dialog.Dialog, error) {
	return s.dialogs.ListByOwner(ctx, ownerID)
}

func (s *Service) Get(ctx context.Context, ownerID, id int64) (*dialog.Dialog, error) {
	item, err := s.dialogs.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return item, nil
}

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateDialogRequest) (*dialog.Dialog, error) {
	item, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, agenterrors.ErrInvalidInput
		}
		item.Name = name
	}
	if req.Description != nil {
		item.Description = strings.TrimSpace(*req.Description)
	}
	if req.ProviderID != nil {
		if *req.ProviderID < 0 {
			return nil, agenterrors.ErrInvalidInput
		}
		item.ProviderID = *req.ProviderID
	}
	if req.Model != nil {
		item.Model = strings.TrimSpace(*req.Model)
	}
	if req.SystemPrompt != nil {
		item.SystemPrompt = strings.TrimSpace(*req.SystemPrompt)
	}
	if req.Prologue != nil {
		item.Prologue = strings.TrimSpace(*req.Prologue)
	}
	if req.KBIDs != nil {
		item.KBIDs = *req.KBIDs
	}
	if req.TopK != nil {
		if *req.TopK < 0 {
			return nil, agenterrors.ErrInvalidInput
		}
		item.TopK = *req.TopK
	}
	if req.RetrievalMode != nil {
		item.RetrievalMode = strings.TrimSpace(*req.RetrievalMode)
	}
	if req.HistoryRoundLimit != nil {
		if *req.HistoryRoundLimit < 0 {
			return nil, agenterrors.ErrInvalidInput
		}
		item.HistoryRoundLimit = *req.HistoryRoundLimit
	}
	if req.Status != nil {
		item.Status = *req.Status
	}
	if err := s.dialogs.Update(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	if _, err := s.Get(ctx, ownerID, id); err != nil {
		return err
	}
	return s.dialogs.SoftDelete(ctx, ownerID, id)
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agenterrors.ErrNotFound
	}
	return err
}
