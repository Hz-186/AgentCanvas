package agent_usecase

import (
	"context"
	"testing"
)

func TestRunCancelRegistryCancelsRegisteredRun(t *testing.T) {
	registry := newRunCancelRegistry()
	ctx, cancel := context.WithCancel(context.Background())

	registry.Register(10, cancel)
	if !registry.Cancel(10) {
		t.Fatal("Cancel() = false, want true")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
	registry.Unregister(10)
	if registry.Cancel(10) {
		t.Fatal("Cancel() after Unregister = true, want false")
	}
}
