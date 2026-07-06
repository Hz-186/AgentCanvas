package config

import "testing"

func TestQueueConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.setDefaults()
	if cfg.Queue.Backend != "mysql" || cfg.Queue.RedisStream == "" || cfg.Queue.RedisGroup == "" || cfg.Queue.RedisConsumer == "" {
		t.Fatalf("unexpected queue defaults: %+v", cfg.Queue)
	}
	if cfg.Milvus.Collection == "" || cfg.Milvus.M == 0 || cfg.Milvus.MetricType != "COSINE" {
		t.Fatalf("unexpected milvus defaults: %+v", cfg.Milvus)
	}
	if cfg.OCR.TimeoutSeconds != 60 {
		t.Fatalf("unexpected OCR defaults: %+v", cfg.OCR)
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
