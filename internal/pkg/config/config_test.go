package config

import "testing"

func TestQueueConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.setDefaults()
	if cfg.Queue.Backend != "mysql" || cfg.Queue.RedisStream == "" || cfg.Queue.RedisGroup == "" || cfg.Queue.RedisConsumer == "" {
		t.Fatalf("unexpected queue defaults: %+v", cfg.Queue)
	}
	if cfg.LLMCache.SimilarityThreshold != 0.96 || cfg.LLMCache.TTLSeconds != 86400 {
		t.Fatalf("unexpected llm cache defaults: %+v", cfg.LLMCache)
	}
	if cfg.ResourceCache.KeyPrefix != "agentcanvas" || cfg.ResourceCache.TTLSeconds != 60 {
		t.Fatalf("unexpected resource cache defaults: %+v", cfg.ResourceCache)
	}
	if cfg.MemoryDream.TriggerEveryNTurns != 5 || cfg.MemoryDream.IdleTimeoutSeconds != 180 {
		t.Fatalf("unexpected memory dream defaults: %+v", cfg.MemoryDream)
	}
	if cfg.NATS.URL == "" || cfg.NATS.Stream == "" || cfg.NATS.Subject == "" || cfg.NATS.Durable == "" || cfg.NATS.AckWaitSeconds == 0 {
		t.Fatalf("unexpected nats defaults: %+v", cfg.NATS)
	}
	if cfg.ReflectionQueue.Backend != "mysql" || cfg.ReflectionQueue.Stream != "AGENTCANVAS_REFLECTION" ||
		cfg.ReflectionQueue.HeartbeatSeconds != 30 || cfg.ReflectionQueue.AckWaitSeconds != 120 ||
		cfg.ReflectionQueue.LeaseSeconds != 180 || cfg.ReflectionQueue.MaxAckPending != cfg.ReflectionQueue.Concurrency {
		t.Fatalf("unexpected reflection queue defaults: %+v", cfg.ReflectionQueue)
	}
	if cfg.Milvus.Collection == "" || cfg.Milvus.M == 0 || cfg.Milvus.MetricType != "COSINE" {
		t.Fatalf("unexpected milvus defaults: %+v", cfg.Milvus)
	}
	if cfg.ContextIndex.BatchSize != 50 || cfg.ContextIndex.PollMilliseconds != 1000 || cfg.ContextIndex.LeaseSeconds != 60 {
		t.Fatalf("unexpected context index defaults: %+v", cfg.ContextIndex)
	}
	if cfg.OCR.TimeoutSeconds != 60 {
		t.Fatalf("unexpected OCR defaults: %+v", cfg.OCR)
	}
}

func TestReflectionQueueConfigValidation(t *testing.T) {
	base := Config{MySQL: MySQLConfig{DSN: "dsn"}, Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"}}
	base.setDefaults()
	base.ReflectionQueue.Backend = "nats"
	if err := base.Validate(); err != nil {
		t.Fatalf("valid reflection queue config rejected: %v", err)
	}

	invalidHeartbeat := base
	invalidHeartbeat.ReflectionQueue.HeartbeatSeconds = invalidHeartbeat.ReflectionQueue.AckWaitSeconds / 2
	if err := invalidHeartbeat.Validate(); err == nil {
		t.Fatal("expected invalid reflection heartbeat to be rejected")
	}

	invalidLease := base
	invalidLease.ReflectionQueue.LeaseSeconds = invalidLease.ReflectionQueue.AckWaitSeconds - 1
	if err := invalidLease.Validate(); err == nil {
		t.Fatal("expected short reflection lease to be rejected")
	}
}

func TestQueueConfigAcceptsNATSBackend(t *testing.T) {
	cfg := Config{
		MySQL:    MySQLConfig{DSN: "dsn"},
		Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		Queue:    QueueConfig{Backend: "nats"},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.NATS.URL != "nats://localhost:4222" || cfg.NATS.Durable != cfg.NATS.Consumer {
		t.Fatalf("unexpected nats defaults: %+v", cfg.NATS)
	}
}

func TestQueueConfigRejectsUnsupportedBackend(t *testing.T) {
	cfg := Config{
		MySQL:    MySQLConfig{DSN: "dsn"},
		Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		Queue:    QueueConfig{Backend: "cmq"},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported queue backend error")
	}
}

func TestMilvusRequiresAddressWhenEnabled(t *testing.T) {
	cfg := Config{
		MySQL:    MySQLConfig{DSN: "dsn"},
		Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		Milvus:   MilvusConfig{Enabled: true},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing milvus address error")
	}
}

func TestOCRRequiresEndpointWhenEnabled(t *testing.T) {
	cfg := Config{
		MySQL:    MySQLConfig{DSN: "dsn"},
		Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		OCR:      OCRConfig{Enabled: true},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing OCR endpoint error")
	}
}

func TestDockerConfigLoadsWithNATSAndMilvus(t *testing.T) {
	cfg, err := LoadConfig("../../../configs/config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig(config.yaml) error = %v", err)
	}
	if cfg.Queue.Backend != "nats" || cfg.NATS.URL != "nats://nats:4222" {
		t.Fatalf("unexpected docker queue config: queue=%+v nats=%+v", cfg.Queue, cfg.NATS)
	}
	if !cfg.Milvus.Enabled || cfg.Milvus.Address != "http://milvus:19530" {
		t.Fatalf("unexpected docker milvus config: %+v", cfg.Milvus)
	}
}
