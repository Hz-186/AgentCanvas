package audit_usecase

import (
	"agentcanvas/internal/domain/audit"
	"context"
)

type Service struct {
	repo audit.Repository
}

func NewService(repo audit.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context, ownerID int64, limit, offset int) ([]audit.Log, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListByOwner(ctx, ownerID, limit, offset)
}
