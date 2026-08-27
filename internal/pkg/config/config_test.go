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
	if cfg.DurableMemory.IdleTimeoutSeconds != 6*60*60 || cfg.DurableMemory.TriggerEveryNTurns != 0 {
		t.Fatalf("unexpected durable memory defaults: %+v", cfg.DurableMemory)
	}
	if cfg.WorkingMemory.TTLSeconds != 0 || cfg.WorkingMemory.LockTTLMS != 0 || cfg.WorkingMemory.LockWaitMS != 0 {
		t.Fatalf("deprecated working memory config must remain inert: %+v", cfg.WorkingMemory)
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

func TestDurableMemoryRequiresUsableProviderConfiguration(t *testing.T) {
	cfg := Config{
		MySQL:         MySQLConfig{DSN: "dsn"},
		Security:      SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		DurableMemory: DurableMemoryConfig{Enabled: true},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled durable memory without providers was accepted")
	}

	cfg.DurableMemory = DurableMemoryConfig{
		Enabled: true, LLMProviderType: "openai", LLMBaseURL: "https://api.example/v1", LLMAPIKey: "key", LLMModel: "chat",
		EmbeddingProviderType: "openai", EmbeddingBaseURL: "https://api.example/v1", EmbeddingModel: "embedding",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("usable durable memory config rejected: %v", err)
	}
}

func TestLegacyMemoryDreamConfigMigratesOnce(t *testing.T) {
	cfg := Config{MemoryDream: MemoryDreamConfig{Enabled: true, LLMProviderType: "openai", LLMBaseURL: "https://api.example/v1", LLMAPIKey: "key", LLMModel: "chat"}}
	cfg.setDefaults()
	if !cfg.DurableMemory.Enabled || cfg.EffectiveDurableMemory().LLMModel != "chat" {
		t.Fatalf("legacy memory_dream was not migrated: %+v", cfg)
	}
}

func TestLegacyCodexMemoryConfigMigratesOnce(t *testing.T) {
	cfg := Config{CodexMemoryLegacy: DurableMemoryConfig{Enabled: true, LLMProviderType: "openai", LLMBaseURL: "https://api.example/v1", LLMAPIKey: "key", LLMModel: "chat"}}
	cfg.setDefaults()
	if !cfg.DurableMemory.Enabled || cfg.EffectiveDurableMemory().LLMModel != "chat" {
		t.Fatalf("legacy codex_memory was not migrated: %+v", cfg)
	}
}

func TestCodexMemoryLegacyWinsOverMemoryDream(t *testing.T) {
	cfg := Config{
		CodexMemoryLegacy: DurableMemoryConfig{Enabled: true, LLMModel: "legacy"},
		MemoryDream:       MemoryDreamConfig{Enabled: true, LLMModel: "dream"},
	}
	cfg.setDefaults()
	if cfg.DurableMemory.LLMModel != "legacy" {
		t.Fatalf("codex_memory alias did not outrank memory_dream: %+v", cfg)
	}
}

func TestExplicitDurableMemoryWinsOverLegacyAliases(t *testing.T) {
	cfg := Config{
		durableMemoryConfigured: true,
		DurableMemory:           DurableMemoryConfig{},
		CodexMemoryLegacy:       DurableMemoryConfig{Enabled: true, LLMModel: "legacy"},
		MemoryDream:             MemoryDreamConfig{Enabled: true, LLMModel: "dream"},
	}
	cfg.setDefaults()
	if cfg.DurableMemory.Enabled || cfg.DurableMemory.LLMModel != "" {
		t.Fatalf("explicit durable_memory section was overridden by aliases: %+v", cfg)
	}
}

func TestDurableMemoryDoesNotRequireEmbeddingProvider(t *testing.T) {
	cfg := Config{
		MySQL:         MySQLConfig{DSN: "dsn"},
		Security:      SecurityConfig{JWTSecret: "jwt", RefreshTokenPepper: "pepper", SecretEncryptKey: "secret"},
		DurableMemory: DurableMemoryConfig{Enabled: true, LLMProviderType: "openai", LLMBaseURL: "https://api.example/v1", LLMAPIKey: "key", LLMModel: "chat"},
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("durable memory config unexpectedly requires embedding: %v", err)
	}
}
