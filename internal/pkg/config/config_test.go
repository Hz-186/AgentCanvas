package config

import "testing"

func TestQueueConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.setDefaults()
	if cfg.Queue.Backend != "mysql" || cfg.Queue.RedisStream == "" || cfg.Queue.RedisGroup == "" || cfg.Queue.RedisConsumer == "" {
		t.Fatalf("unexpected queue defaults: %+v", cfg.Queue)
	}
	if cfg.LLMCache.TTLSeconds != 86400 {
		t.Fatalf("unexpected llm cache defaults: %+v", cfg.LLMCache)
	}
	if cfg.ResourceCache.KeyPrefix != "agentcanvas" || cfg.ResourceCache.TTLSeconds != 60 {
		t.Fatalf("unexpected resource cache defaults: %+v", cfg.ResourceCache)
	}
	if cfg.MemoryDream.TriggerEveryNTurns != 5 || cfg.MemoryDream.IdleTimeoutSeconds != 180 {
		t.Fatalf("unexpected memory dream defaults: %+v", cfg.MemoryDream)
	}
	if cfg.WorkingMemory.TTLSeconds != 86400 || cfg.WorkingMemory.LockTTLMS != 5000 || cfg.WorkingMemory.LockWaitMS != 500 {
		t.Fatalf("unexpected working memory defaults: %+v", cfg.WorkingMemory)
	}
	if cfg.NATS.URL == "" || cfg.NATS.Stream == "" || cfg.NATS.Subject == "" || cfg.NATS.Durable == "" || cfg.NATS.AckWaitSeconds == 0 {
		t.Fatalf("unexpected nats defaults: %+v", cfg.NATS)
	}
	if cfg.ReflectionQueue.Backend != "mysql" || cfg.ReflectionQueue.Stream != "AGENTCANVAS_REFLECTION" ||
		cfg.ReflectionQueue.HeartbeatSeconds != 30 || cfg.ReflectionQueue.AckWaitSeconds != 120 ||
		cfg.ReflectionQueue.LeaseSeconds != 180 || cfg.ReflectionQueue.MaxAckPending != cfg.ReflectionQueue.Concurrency {
		t.Fatalf("unexpected reflection queue defaults: %+v", cfg.ReflectionQueue)
	}
	if cfg.Milvus.Collection != "agentcanvas_chunks_v2" || cfg.Milvus.M == 0 || cfg.Milvus.MetricType != "COSINE" {
		t.Fatalf("unexpected milvus defaults: %+v", cfg.Milvus)
	}
	if cfg.Retrieval.Backend != "elasticsearch" {
		t.Fatalf("unexpected retrieval backend default: %+v", cfg.Retrieval)
	}
	if cfg.ContextIndex.BatchSize != 50 || cfg.ContextIndex.PollMilliseconds != 1000 || cfg.ContextIndex.LeaseSeconds != 60 {
		t.Fatalf("unexpected context index defaults: %+v", cfg.ContextIndex)
	}
	if cfg.OCR.TimeoutSeconds != 60 {
		t.Fatalf("unexpected OCR defaults: %+v", cfg.OCR)
	}
	if cfg.GitWorkspace.WorktreeDirName != ".worktrees" || cfg.GitWorkspace.FetchTimeoutSeconds != 5 ||
		cfg.GitWorkspace.FetchFreshnessSeconds != 300 || cfg.GitWorkspace.GitCommandTimeoutSeconds != 30 ||
		cfg.GitWorkspace.MaxOutputBytes != 256*1024 || cfg.GitWorkspace.FileReadMaxChars != 100000 ||
		cfg.GitWorkspace.MaxWorkspacesPerProject != 64 || cfg.GitWorkspace.PruneTTLHours != 24 {
		t.Fatalf("unexpected Git workspace defaults: %+v", cfg.GitWorkspace)
	}
	if cfg.PythonBridge.Target != "127.0.0.1:50051" || cfg.PythonBridge.MaxConcurrency != 8 || cfg.PythonBridge.DocumentParser != "go" || cfg.PythonBridge.MaxDocumentBytes != 8*1024*1024 {
		t.Fatalf("unexpected Python bridge defaults: %+v", cfg.PythonBridge)
	}
}

func TestPythonBridgeConfigAcceptsLangChainMethods(t *testing.T) {
	cfg := Config{
		MySQL:    MySQLConfig{DSN: "dsn"},
		Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		PythonBridge: PythonBridgeConfig{
			Enabled: true, AllowExperimentalParsing: true, DocumentParser: "python:langchain_pdf",
			AllowedParserMethods: []string{"python:langchain_pdf"},
			AllowedChunkMethods:  []string{"python:langchain_recursive"},
		},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPythonBridgeConfigRejectsParserWithoutBridge(t *testing.T) {
	cfg := Config{PythonBridge: PythonBridgeConfig{DocumentParser: "python:langchain_pdf"}}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected Python parser to require bridge")
	}
}

func TestPythonBridgeConfigRejectsUnsupportedChunker(t *testing.T) {
	cfg := Config{
		MySQL:        MySQLConfig{DSN: "dsn"},
		Security:     SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		PythonBridge: PythonBridgeConfig{Enabled: true, AllowedChunkMethods: []string{"python:unknown"}},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported Python chunk method error")
	}
}

func TestPythonBridgeConfigRejectsShadowWithoutBridge(t *testing.T) {
	cfg := Config{PythonBridge: PythonBridgeConfig{ShadowEnabled: true}}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected shadow mode to require Python bridge")
	}
}

func TestGitWorkspaceConfigRequiresSafeAbsoluteRootAndDirectoryName(t *testing.T) {
	base := Config{MySQL: MySQLConfig{DSN: "dsn"}, Security: SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"}}
	base.setDefaults()
	base.GitWorkspace.Enabled = true
	base.GitWorkspace.AllowedRoots = []string{"/workspaces"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid Git workspace config rejected: %v", err)
	}

	relativeRoot := base
	relativeRoot.GitWorkspace.AllowedRoots = []string{"workspaces"}
	if err := relativeRoot.Validate(); err == nil {
		t.Fatal("relative allowed root was accepted")
	}

	escapingDirectory := base
	escapingDirectory.GitWorkspace.WorktreeDirName = "../worktrees"
	if err := escapingDirectory.Validate(); err == nil {
		t.Fatal("escaping worktree directory name was accepted")
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

func TestMilvusRequiresAddressWhenSelected(t *testing.T) {
	cfg := Config{
		MySQL:     MySQLConfig{DSN: "dsn"},
		Security:  SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		Retrieval: RetrievalConfig{Backend: "milvus"},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing milvus address error")
	}
}

func TestMilvusRequiresDimensionsWhenSelected(t *testing.T) {
	cfg := Config{
		MySQL:     MySQLConfig{DSN: "dsn"},
		Security:  SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		Retrieval: RetrievalConfig{Backend: "milvus"},
		Milvus:    MilvusConfig{Address: "http://milvus:19530"},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing milvus dimensions error")
	}
}

func TestRetrievalBackendRejectsUnsupportedValue(t *testing.T) {
	cfg := Config{Retrieval: RetrievalConfig{Backend: "solr"}}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported retrieval backend error")
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
	t.Setenv("AGENTCANVAS_MYSQL_DSN", "agentcanvas:password@tcp(mysql:3306)/agentcanvas")
	t.Setenv("AGENTCANVAS_REDIS_PASSWORD", "redis-password")
	t.Setenv("AGENTCANVAS_MINIO_ACCESS_KEY", "minio-access")
	t.Setenv("AGENTCANVAS_MINIO_SECRET_KEY", "minio-secret")
	t.Setenv("AGENTCANVAS_JWT_SECRET", "docker-jwt-secret")
	t.Setenv("AGENTCANVAS_REFRESH_TOKEN_PEPPER", "docker-refresh-pepper")
	t.Setenv("AGENTCANVAS_SECRET_ENCRYPT_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
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

func TestDockerConfigRejectsUnresolvedSecrets(t *testing.T) {
	cfg := Config{
		App:      AppConfig{Env: "docker", CORSAllowedOrigins: []string{"http://localhost:8080"}},
		MySQL:    MySQLConfig{DSN: "${AGENTCANVAS_MYSQL_DSN}"},
		Security: SecurityConfig{JWTSecret: "${AGENTCANVAS_JWT_SECRET}", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("docker config accepted unresolved secrets")
	}
}

func TestProductionConfigRejectsPlaceholderSecretsAndWildcardCORS(t *testing.T) {
	base := Config{
		App:      AppConfig{Env: "production", CORSAllowedOrigins: []string{"https://agentcanvas.example"}},
		MySQL:    MySQLConfig{DSN: "dsn"},
		Security: SecurityConfig{JWTSecret: "secure-jwt", RefreshTokenPepper: "secure-pepper", SecretEncryptKey: "secure-key"},
	}
	base.setDefaults()
	if err := base.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}

	placeholder := base
	placeholder.Security.JWTSecret = "change-this-jwt-secret"
	if err := placeholder.Validate(); err == nil {
		t.Fatal("production placeholder secret was accepted")
	}

	wildcard := base
	wildcard.App.CORSAllowedOrigins = []string{"*"}
	if err := wildcard.Validate(); err == nil {
		t.Fatal("production wildcard CORS was accepted")
	}
}

func TestMemoryDreamRequiresUsableProviderConfiguration(t *testing.T) {
	cfg := Config{
		MySQL:       MySQLConfig{DSN: "dsn"},
		Security:    SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		MemoryDream: MemoryDreamConfig{Enabled: true},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled memory dream without providers was accepted")
	}

	cfg.MemoryDream = MemoryDreamConfig{
		Enabled: true, LLMProviderType: "openai", LLMBaseURL: "https://api.example/v1", LLMAPIKey: "key", LLMModel: "chat",
		EmbeddingProviderType: "openai", EmbeddingBaseURL: "https://api.example/v1", EmbeddingModel: "embedding",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("usable memory dream config rejected: %v", err)
	}
}
