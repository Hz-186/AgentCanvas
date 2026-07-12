package resource_usecase

import (
	"context"

	"agentcanvas/internal/domain/resource"
)

type Service struct {
	query resource.Query
}

func NewService(query resource.Query) *Service {
	return &Service{query: query}
}

func (s *Service) List(ctx context.Context, ownerID int64, kind resource.Kind, options resource.ListOptions) (resource.Page, error) {
	return s.query.List(ctx, ownerID, kind, options)
}
