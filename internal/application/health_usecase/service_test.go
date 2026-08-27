package health_usecase

import (
	"context"
	"testing"
)

type backendFake struct{ checked string }

func (b *backendFake) Check(_ context.Context, component string) error {
	b.checked = component
	return nil
}
func (*backendFake) ContextSystem(context.Context) (map[string]any, error) {
	return map[string]any{"component": "context_system"}, nil
}
func (*backendFake) MemorySystem(context.Context) (map[string]any, error) {
	return map[string]any{"component": "memory_system"}, nil
}

func TestServiceDelegatesHealthChecks(t *testing.T) {
	backend := &backendFake{}
	service := NewService(backend)
	if err := service.Check(context.Background(), "mysql"); err != nil || backend.checked != "mysql" {
		t.Fatalf("health check was not delegated: component=%q error=%v", backend.checked, err)
	}
	if snapshot, err := service.MemorySystem(context.Background()); err != nil || snapshot["component"] != "memory_system" {
		t.Fatalf("memory snapshot was not delegated: snapshot=%v error=%v", snapshot, err)
	}
}
