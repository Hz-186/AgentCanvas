package queue

import (
	"context"
	"strings"
	"testing"

	"agentcanvas/internal/pkg/config"
)

func TestNewConfiguredJobQueueUsesMySQLByDefault(t *testing.T) {
	cfg := &config.Config{}
	cfg.Queue.Backend = "mysql"

	q, err := NewConfiguredJobQueue(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("NewConfiguredJobQueue() error = %v", err)
	}
	if q != nil {
		t.Fatalf("mysql backend should use DB polling, got %T", q)
	}
}

func TestNewConfiguredJobQueueBuildsRedisStreamQueue(t *testing.T) {
	cfg := &config.Config{}
	cfg.Queue.Backend = "redis_stream"

	_, err := NewConfiguredJobQueue(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "redis client is required") {
		t.Fatalf("expected redis client error, got %v", err)
	}
}

func TestNewConfiguredJobQueueRejectsUnsupportedBackend(t *testing.T) {
	cfg := &config.Config{}
	cfg.Queue.Backend = "kafka"

	_, err := NewConfiguredJobQueue(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported queue backend") {
		t.Fatalf("expected unsupported backend error, got %v", err)
	}
}
