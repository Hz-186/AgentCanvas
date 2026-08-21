package health_usecase

import (
	"context"
	"fmt"
)

type Backend interface {
	Check(ctx context.Context, component string) error
	ReflectionSystem(ctx context.Context) (map[string]any, error)
	ContextSystem(ctx context.Context) (map[string]any, error)
	MemorySystem(ctx context.Context) (map[string]any, error)
}

type Service struct{ backend Backend }

func NewService(backend Backend) *Service { return &Service{backend: backend} }

func (s *Service) Check(ctx context.Context, component string) error {
	if s == nil || s.backend == nil {
		return fmt.Errorf("health backend is not configured")
	}
	return s.backend.Check(ctx, component)
}

func (s *Service) ReflectionSystem(ctx context.Context) (map[string]any, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("health backend is not configured")
	}
	return s.backend.ReflectionSystem(ctx)
}

func (s *Service) ContextSystem(ctx context.Context) (map[string]any, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("health backend is not configured")
	}
	return s.backend.ContextSystem(ctx)
}

func (s *Service) MemorySystem(ctx context.Context) (map[string]any, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("health backend is not configured")
	}
	return s.backend.MemorySystem(ctx)
}
