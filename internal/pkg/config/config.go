package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App           AppConfig           `yaml:"app"`
	MySQL         MySQLConfig         `yaml:"mysql"`
	Redis         RedisConfig         `yaml:"redis"`
	Queue         QueueConfig         `yaml:"queue"`
	LLMCache      LLMCacheConfig      `yaml:"llm_cache"`
	ResourceCache ResourceCacheConfig `yaml:"resource_cache"`
	// CodexMemory is the active durable-memory worker configuration.
	CodexMemory CodexMemoryConfig `yaml:"codex_memory"`
	// MemoryDream is a parse-only compatibility alias for pre-Codex deployments.
	// setDefaults migrates it into CodexMemory before production wiring starts.
	MemoryDream MemoryDreamConfig `yaml:"memory_dream"`
	// WorkingMemory is accepted for backwards-compatible config parsing only.
	// Runtime continuity is provided by conversation history/snapshots; no
	// production component reads this field. Remove it after the migration
	// window closes.
	WorkingMemory         WorkingMemoryConfig   `yaml:"working_memory"`
	NATS                  NATSConfig            `yaml:"nats"`
	ReflectionQueue       ReflectionQueueConfig `yaml:"reflection_queue"`
	AgentRuntime          AgentRuntimeConfig    `yaml:"agent_runtime"`
	Tools                 ToolsConfig           `yaml:"tools"`
	Goals                 GoalsConfig           `yaml:"goals"`
	MinIO                 MinIOConfig           `yaml:"minio"`
	Retrieval             RetrievalConfig       `yaml:"retrieval"`
	Elasticsearch         ElasticsearchConfig   `yaml:"elasticsearch"`
	Milvus                MilvusConfig          `yaml:"milvus"`
	ContextIndex          ContextIndexConfig    `yaml:"context_index"`
	OCR                   OCRConfig             `yaml:"ocr"`
	PythonBridge          PythonBridgeConfig    `yaml:"python_bridge"`
	Security              SecurityConfig        `yaml:"security"`
	OAuth                 OAuthConfig           `yaml:"oauth"`
	GitWorkspace          GitWorkspaceConfig    `yaml:"git_workspace"`
	codexMemoryConfigured bool
}

// UnmarshalYAML records whether codex_memory was present so an explicit
// enabled: false cannot be overridden by the deprecated memory_dream alias.
func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	type plainConfig Config
	var decoded plainConfig
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*c = Config(decoded)
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "codex_memory" {
				c.codexMemoryConfigured = true
				break
			}
		}
	}
	return nil
}

type GitWorkspaceConfig struct {
	Enabled                  bool     `yaml:"enabled"`
	AllowedRoots             []string `yaml:"allowed_roots"`
	WorktreeDirName          string   `yaml:"worktree_dir_name"`
	FetchTimeoutSeconds      int      `yaml:"fetch_timeout_seconds"`
	FetchFreshnessSeconds    int      `yaml:"fetch_freshness_seconds"`
	GitCommandTimeoutSeconds int      `yaml:"git_command_timeout_seconds"`
	MaxOutputBytes           int      `yaml:"max_output_bytes"`
	FileReadMaxChars         int      `yaml:"file_read_max_chars"`
	MaxWorkspacesPerProject  int      `yaml:"max_workspaces_per_project"`
	PruneTTLHours            int      `yaml:"prune_ttl_hours"`
	PreserveDirty            bool     `yaml:"preserve_dirty"`
	PreserveUnpushed         bool     `yaml:"preserve_unpushed"`
	AutoInitRepository       bool     `yaml:"auto_init_repository"`
	GitUserName              string   `yaml:"git_user_name"`
	GitUserEmail             string   `yaml:"git_user_email"`
}

type AgentRuntimeConfig struct {
	WorkerEnabled           bool   `yaml:"worker_enabled"`
	WorkerConcurrency       int    `yaml:"worker_concurrency"`
	LeaseSeconds            int    `yaml:"lease_seconds"`
	SelfImprovementEnabled  bool   `yaml:"self_improvement_enabled"`
	MemoryReviewMode        string `yaml:"memory_review_mode"`
	ReviewWorkerConcurrency int    `yaml:"review_worker_concurrency"`
	ReviewProviderID        int64  `yaml:"review_provider_id"`
	ReviewModel             string `yaml:"review_model"`
}

type ToolsConfig struct {
	UpdatePlan                  ToolToggleConfig `yaml:"update_plan"`
	RequestUserInput            ToolToggleConfig `yaml:"request_user_input"`
	DefaultModeRequestUserInput ToolToggleConfig `yaml:"default_mode_request_user_input"`
}

type GoalsConfig struct {
	MaxTokenBudget *int64 `yaml:"max_goal_token_budget"`
}

type ToolToggleConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type AppConfig struct {
	Name               string   `yaml:"name"`
	Env                string   `yaml:"env"`
	Port               int      `yaml:"port"`
	BaseURL            string   `yaml:"base_url"`
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

type MySQLConfig struct {
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type QueueConfig struct {
	Backend       string `yaml:"backend"`
	RedisStream   string `yaml:"redis_stream"`
	RedisGroup    string `yaml:"redis_group"`
	RedisConsumer string `yaml:"redis_consumer"`
}

type LLMCacheConfig struct {
	Enabled    bool `yaml:"enabled"`
	TTLSeconds int  `yaml:"ttl_seconds"`
}

type ResourceCacheConfig struct {
	Enabled    bool   `yaml:"enabled"`
	KeyPrefix  string `yaml:"key_prefix"`
	TTLSeconds int    `yaml:"ttl_seconds"`
}

type CodexMemoryConfig struct {
	Enabled bool `yaml:"enabled"`
	// Deprecated: Codex extraction is idle-driven and ignores turn counts.
	TriggerEveryNTurns int    `yaml:"trigger_every_n_turns"`
	IdleTimeoutSeconds int    `yaml:"idle_timeout_seconds"`
	LLMProviderType    string `yaml:"llm_provider_type"`
	LLMBaseURL         string `yaml:"llm_base_url"`
	LLMAPIKey          string `yaml:"llm_api_key"`
	LLMModel           string `yaml:"llm_model"`
	// Deprecated: the file-backed Codex pipeline does not use embeddings.
	EmbeddingProviderType string `yaml:"embedding_provider_type"`
	EmbeddingBaseURL      string `yaml:"embedding_base_url"`
	EmbeddingAPIKey       string `yaml:"embedding_api_key"`
	EmbeddingModel        string `yaml:"embedding_model"`
}

// MemoryDreamConfig is kept as a source-compatible alias while deployments
// migrate from memory_dream to codex_memory.
// Deprecated: use CodexMemoryConfig.
type MemoryDreamConfig = CodexMemoryConfig

// WorkingMemoryConfig is retained only so existing deployments keep parsing.
// Deprecated: the runtime ignores this section; use conversation snapshots and
// Codex file-backed memory instead. It is intentionally not defaulted.
type WorkingMemoryConfig struct {
	TTLSeconds int `yaml:"ttl_seconds"`
	LockTTLMS  int `yaml:"lock_ttl_ms"`
	LockWaitMS int `yaml:"lock_wait_ms"`
}

type NATSConfig struct {
	URL                  string `yaml:"url"`
	Stream               string `yaml:"stream"`
	Subject              string `yaml:"subject"`
	Consumer             string `yaml:"consumer"`
	Durable              string `yaml:"durable"`
	AckWaitSeconds       int    `yaml:"ack_wait_seconds"`
	CredentialsFile      string `yaml:"credentials_file"`
	TLSCAFile            string `yaml:"tls_ca_file"`
	TLSCertFile          string `yaml:"tls_cert_file"`
	TLSKeyFile           string `yaml:"tls_key_file"`
	ReconnectWaitSeconds int    `yaml:"reconnect_wait_seconds"`
}

type ReflectionQueueConfig struct {
	Backend                string `yaml:"backend"`
	Stream                 string `yaml:"stream"`
	Subject                string `yaml:"subject"`
	DLQStream              string `yaml:"dlq_stream"`
	DLQSubject             string `yaml:"dlq_subject"`
	Durable                string `yaml:"durable"`
	AckWaitSeconds         int    `yaml:"ack_wait_seconds"`
	HeartbeatSeconds       int    `yaml:"heartbeat_seconds"`
	LeaseSeconds           int    `yaml:"lease_seconds"`
	Concurrency            int    `yaml:"concurrency"`
	MaxAckPending          int    `yaml:"max_ack_pending"`
	OutboxBatchSize        int    `yaml:"outbox_batch_size"`
	OutboxPollMilliseconds int    `yaml:"outbox_poll_milliseconds"`
	StreamMaxAgeDays       int    `yaml:"stream_max_age_days"`
	StreamMaxBytes         int64  `yaml:"stream_max_bytes"`
	StreamReplicas         int    `yaml:"stream_replicas"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type ElasticsearchConfig struct {
	Addresses    []string `yaml:"addresses"`
	Username     string   `yaml:"username"`
	Password     string   `yaml:"password"`
	ChunkIndex   string   `yaml:"chunk_index"`
	MessageIndex string   `yaml:"message_index"`
	ContextIndex string   `yaml:"context_index"`
}

type RetrievalConfig struct {
	Backend string `yaml:"backend"`
}

type MilvusConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Address        string `yaml:"address"`
	Token          string `yaml:"token"`
	Collection     string `yaml:"collection"`
	Dimensions     int    `yaml:"dimensions"`
	M              int    `yaml:"m"`
	EFConstruction int    `yaml:"ef_construction"`
	EFSearch       int    `yaml:"ef_search"`
	MetricType     string `yaml:"metric_type"`
}

type ContextIndexConfig struct {
	Enabled             bool   `yaml:"enabled"`
	WorkerEnabled       bool   `yaml:"worker_enabled"`
	EmbeddingProviderID int64  `yaml:"embedding_provider_id"`
	EmbeddingModel      string `yaml:"embedding_model"`
	BatchSize           int    `yaml:"batch_size"`
	PollMilliseconds    int    `yaml:"poll_milliseconds"`
	LeaseSeconds        int    `yaml:"lease_seconds"`
}

type OCRConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Endpoint       string `yaml:"endpoint"`
	Token          string `yaml:"token"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type PythonBridgeConfig struct {
	Enabled                   bool     `yaml:"enabled"`
	ShadowEnabled             bool     `yaml:"shadow_enabled"`
	AllowExperimentalChunking bool     `yaml:"allow_experimental_chunking"`
	DocumentParser            string   `yaml:"document_parser"`
	ShadowDocumentParser      bool     `yaml:"shadow_document_parser"`
	AllowExperimentalParsing  bool     `yaml:"allow_experimental_parsing"`
	FallbackToGoOCR           bool     `yaml:"fallback_to_go_ocr"`
	Target                    string   `yaml:"target"`
	AuthTokenEnv              string   `yaml:"auth_token_env"`
	ConnectTimeoutSeconds     int      `yaml:"connect_timeout_seconds"`
	RequestTimeoutSeconds     int      `yaml:"request_timeout_seconds"`
	MaxSendBytes              int      `yaml:"max_send_bytes"`
	MaxReceiveBytes           int      `yaml:"max_receive_bytes"`
	MaxConcurrency            int      `yaml:"max_concurrency"`
	AllowedChunkMethods       []string `yaml:"allowed_chunk_methods"`
	AllowedParserMethods      []string `yaml:"allowed_parser_methods"`
	MaxDocumentBytes          int      `yaml:"max_document_bytes"`
}

type SecurityConfig struct {
	JWTSecret             string `yaml:"jwt_secret"`
	RefreshTokenPepper    string `yaml:"refresh_token_pepper"`
	SecretEncryptKey      string `yaml:"secret_encrypt_key"`
	AccessTokenTTLMinutes int    `yaml:"access_token_ttl_minutes"`
	RefreshTokenTTLDays   int    `yaml:"refresh_token_ttl_days"`
}

type OAuthConfig struct {
	GitHub GitHubOAuthConfig `yaml:"github"`
}

type GitHubOAuthConfig struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config

	data = []byte(os.Expand(string(data), func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return "${" + key + "}"
	}))
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.setDefaults()
	return &cfg, cfg.Validate()
}

// EffectiveCodexMemory returns the sole runtime durable-memory configuration.
// Legacy memory_dream values are normalized into this field during loading.
func (c *Config) EffectiveCodexMemory() CodexMemoryConfig {
	if c == nil {
		return CodexMemoryConfig{}
	}
	return c.CodexMemory
}

func isZeroCodexMemory(cfg CodexMemoryConfig) bool {
	return !cfg.Enabled && cfg.TriggerEveryNTurns == 0 && cfg.IdleTimeoutSeconds == 0 &&
		cfg.LLMProviderType == "" && cfg.LLMBaseURL == "" && cfg.LLMAPIKey == "" && cfg.LLMModel == "" &&
		cfg.EmbeddingProviderType == "" && cfg.EmbeddingBaseURL == "" && cfg.EmbeddingAPIKey == "" && cfg.EmbeddingModel == ""
}

func (c *Config) setDefaults() {
	// Migrate the old YAML section once at load time. If both sections are
	// present, codex_memory wins, including an explicit enabled: false.
	if !c.codexMemoryConfigured && isZeroCodexMemory(c.CodexMemory) && !isZeroCodexMemory(c.MemoryDream) {
		c.CodexMemory = c.MemoryDream
	}
	if c.App.Name == "" {
		c.App.Name = "agentcanvas"
	}
	if c.App.Env == "" {
		c.App.Env = "local"
	}
	if c.App.Port == 0 {
		c.App.Port = 8080
	}
	if len(c.App.CORSAllowedOrigins) == 0 && c.App.BaseURL != "" {
		c.App.CORSAllowedOrigins = []string{c.App.BaseURL}
	}
	if c.Security.AccessTokenTTLMinutes == 0 {
		c.Security.AccessTokenTTLMinutes = 30
	}
	if c.Security.RefreshTokenTTLDays == 0 {
		c.Security.RefreshTokenTTLDays = 30
	}
	if len(c.OAuth.GitHub.Scopes) == 0 {
		c.OAuth.GitHub.Scopes = []string{"read:user", "user:email"}
	}
	if c.OAuth.GitHub.RedirectURL == "" && c.App.BaseURL != "" {
		c.OAuth.GitHub.RedirectURL = c.App.BaseURL + "/api/v1/auth/github/callback"
	}
	if c.Elasticsearch.ChunkIndex == "" {
		c.Elasticsearch.ChunkIndex = "agentcanvas_chunks_v1"
	}
	if c.Retrieval.Backend == "" {
		c.Retrieval.Backend = "elasticsearch"
	}
	if c.Elasticsearch.MessageIndex == "" {
		c.Elasticsearch.MessageIndex = "agentcanvas_messages_v1"
	}
	if c.Elasticsearch.ContextIndex == "" {
		c.Elasticsearch.ContextIndex = "agentcanvas_context_resources_v1"
	}
	if c.Queue.Backend == "" {
		c.Queue.Backend = "mysql"
	}
	if c.Queue.RedisStream == "" {
		c.Queue.RedisStream = "agentcanvas:jobs"
	}
	if c.Queue.RedisGroup == "" {
		c.Queue.RedisGroup = "agentcanvas-workers"
	}
	if c.Queue.RedisConsumer == "" {
		c.Queue.RedisConsumer = "worker"
	}
	if c.LLMCache.TTLSeconds == 0 {
		c.LLMCache.TTLSeconds = 86400
	}
	if c.ResourceCache.KeyPrefix == "" {
		c.ResourceCache.KeyPrefix = "agentcanvas"
	}
	if c.ResourceCache.TTLSeconds == 0 {
		c.ResourceCache.TTLSeconds = 60
	}
	// Codex consolidation is idle-driven; it has no turn-count trigger. Keep
	// the longer Codex idle default separate from the legacy Dream alias.
	if c.CodexMemory.IdleTimeoutSeconds == 0 {
		c.CodexMemory.IdleTimeoutSeconds = 6 * 60 * 60
	}
	if c.NATS.URL == "" {
		c.NATS.URL = "nats://localhost:4222"
	}
	if c.NATS.Stream == "" {
		c.NATS.Stream = "AGENTCANVAS_INGESTION"
	}
	if c.NATS.Subject == "" {
		c.NATS.Subject = "agentcanvas.ingestion"
	}
	if c.NATS.Consumer == "" {
		c.NATS.Consumer = "agentcanvas-workers"
	}
	if c.NATS.Durable == "" {
		c.NATS.Durable = c.NATS.Consumer
	}
	if c.NATS.AckWaitSeconds == 0 {
		c.NATS.AckWaitSeconds = 60
	}
	if c.NATS.ReconnectWaitSeconds == 0 {
		c.NATS.ReconnectWaitSeconds = 2
	}
	if c.ReflectionQueue.Backend == "" {
		c.ReflectionQueue.Backend = "mysql"
	}
	if c.ReflectionQueue.Stream == "" {
		c.ReflectionQueue.Stream = "AGENTCANVAS_REFLECTION"
	}
	if c.ReflectionQueue.Subject == "" {
		c.ReflectionQueue.Subject = "agentcanvas.reflection.jobs"
	}
	if c.ReflectionQueue.DLQStream == "" {
		c.ReflectionQueue.DLQStream = "AGENTCANVAS_REFLECTION_DLQ"
	}
	if c.ReflectionQueue.DLQSubject == "" {
		c.ReflectionQueue.DLQSubject = "agentcanvas.reflection.dlq"
	}
	if c.ReflectionQueue.Durable == "" {
		c.ReflectionQueue.Durable = "reflection-workers"
	}
	if c.ReflectionQueue.AckWaitSeconds == 0 {
		c.ReflectionQueue.AckWaitSeconds = 120
	}
	if c.ReflectionQueue.HeartbeatSeconds == 0 {
		c.ReflectionQueue.HeartbeatSeconds = 30
	}
	if c.ReflectionQueue.LeaseSeconds == 0 {
		c.ReflectionQueue.LeaseSeconds = 180
	}
	if c.ReflectionQueue.Concurrency == 0 {
		c.ReflectionQueue.Concurrency = 2
	}
	if c.ReflectionQueue.MaxAckPending == 0 {
		c.ReflectionQueue.MaxAckPending = c.ReflectionQueue.Concurrency
	}
	if c.ReflectionQueue.OutboxBatchSize == 0 {
		c.ReflectionQueue.OutboxBatchSize = 100
	}
	if c.ReflectionQueue.OutboxPollMilliseconds == 0 {
		c.ReflectionQueue.OutboxPollMilliseconds = 500
	}
	if c.ReflectionQueue.StreamMaxAgeDays == 0 {
		c.ReflectionQueue.StreamMaxAgeDays = 30
	}
	if c.ReflectionQueue.StreamMaxBytes == 0 {
		c.ReflectionQueue.StreamMaxBytes = 1 << 30
	}
	if c.ReflectionQueue.StreamReplicas == 0 {
		c.ReflectionQueue.StreamReplicas = 1
	}
	if c.AgentRuntime.WorkerConcurrency == 0 {
		c.AgentRuntime.WorkerConcurrency = 2
	}
	if c.AgentRuntime.LeaseSeconds == 0 {
		c.AgentRuntime.LeaseSeconds = 30
	}
	if c.AgentRuntime.MemoryReviewMode == "" {
		c.AgentRuntime.MemoryReviewMode = "suggest"
	}
	if c.AgentRuntime.ReviewWorkerConcurrency == 0 {
		c.AgentRuntime.ReviewWorkerConcurrency = 1
	}
	if c.Milvus.Collection == "" {
		c.Milvus.Collection = "agentcanvas_chunks_v2"
	}
	if c.Milvus.M == 0 {
		c.Milvus.M = 16
	}
	if c.Milvus.EFConstruction == 0 {
		c.Milvus.EFConstruction = 200
	}
	if c.Milvus.EFSearch == 0 {
		c.Milvus.EFSearch = 64
	}
	if c.Milvus.MetricType == "" {
		c.Milvus.MetricType = "COSINE"
	}
	if c.ContextIndex.BatchSize == 0 {
		c.ContextIndex.BatchSize = 50
	}
	if c.ContextIndex.PollMilliseconds == 0 {
		c.ContextIndex.PollMilliseconds = 1000
	}
	if c.ContextIndex.LeaseSeconds == 0 {
		c.ContextIndex.LeaseSeconds = 60
	}
	if c.OCR.TimeoutSeconds == 0 {
		c.OCR.TimeoutSeconds = 60
	}
	if c.GitWorkspace.WorktreeDirName == "" {
		c.GitWorkspace.WorktreeDirName = ".worktrees"
	}
	if c.GitWorkspace.FetchTimeoutSeconds == 0 {
		c.GitWorkspace.FetchTimeoutSeconds = 5
	}
	if c.GitWorkspace.FetchFreshnessSeconds == 0 {
		c.GitWorkspace.FetchFreshnessSeconds = 300
	}
	if c.GitWorkspace.GitCommandTimeoutSeconds == 0 {
		c.GitWorkspace.GitCommandTimeoutSeconds = 30
	}
	if c.GitWorkspace.MaxOutputBytes == 0 {
		c.GitWorkspace.MaxOutputBytes = 256 * 1024
	}
	if c.GitWorkspace.FileReadMaxChars == 0 {
		c.GitWorkspace.FileReadMaxChars = 100000
	}
	if c.GitWorkspace.MaxWorkspacesPerProject == 0 {
		c.GitWorkspace.MaxWorkspacesPerProject = 64
	}
	if c.GitWorkspace.PruneTTLHours == 0 {
		c.GitWorkspace.PruneTTLHours = 24
	}
	if c.GitWorkspace.GitUserName == "" {
		c.GitWorkspace.GitUserName = "AgentCanvas"
	}
	if c.GitWorkspace.GitUserEmail == "" {
		c.GitWorkspace.GitUserEmail = "agentcanvas@localhost"
	}
	if c.PythonBridge.Target == "" {
		c.PythonBridge.Target = "127.0.0.1:50051"
	}
	if c.PythonBridge.AuthTokenEnv == "" {
		c.PythonBridge.AuthTokenEnv = "AGENTCANVAS_PYTHON_BRIDGE_TOKEN"
	}
	if c.PythonBridge.ConnectTimeoutSeconds == 0 {
		c.PythonBridge.ConnectTimeoutSeconds = 2
	}
	if c.PythonBridge.RequestTimeoutSeconds == 0 {
		c.PythonBridge.RequestTimeoutSeconds = 30
	}
	if c.PythonBridge.MaxSendBytes == 0 {
		c.PythonBridge.MaxSendBytes = 8 * 1024 * 1024
	}
	if c.PythonBridge.MaxReceiveBytes == 0 {
		c.PythonBridge.MaxReceiveBytes = 2 * 1024 * 1024
	}
	if c.PythonBridge.MaxConcurrency == 0 {
		c.PythonBridge.MaxConcurrency = 8
	}
	if c.PythonBridge.DocumentParser == "" {
		c.PythonBridge.DocumentParser = "go"
	}
	if c.PythonBridge.MaxDocumentBytes == 0 {
		c.PythonBridge.MaxDocumentBytes = c.PythonBridge.MaxSendBytes
	}
}

func (c *Config) Validate() error {
	if c.MySQL.DSN == "" {
		return fmt.Errorf("mysql.dsn is required")
	}
	if c.Security.JWTSecret == "" {
		return fmt.Errorf("security.jwt_secret is required")
	}
	if c.Security.RefreshTokenPepper == "" {
		return fmt.Errorf("security.refresh_token_pepper is required")
	}
	if c.Security.SecretEncryptKey == "" {
		return fmt.Errorf("security.secret_encrypt_key is required")
	}
	productionLike := strings.EqualFold(c.App.Env, "production") || strings.EqualFold(c.App.Env, "docker")
	for _, origin := range c.App.CORSAllowedOrigins {
		if strings.TrimSpace(origin) == "" {
			return fmt.Errorf("app.cors_allowed_origins must not contain empty origins")
		}
		if strings.TrimSpace(origin) == "*" && productionLike {
			return fmt.Errorf("wildcard CORS origin is forbidden in production")
		}
	}
	for name, value := range map[string]string{
		"mysql.dsn": c.MySQL.DSN, "redis.password": c.Redis.Password,
		"minio.access_key": c.MinIO.AccessKey, "minio.secret_key": c.MinIO.SecretKey,
		"security.jwt_secret": c.Security.JWTSecret, "security.refresh_token_pepper": c.Security.RefreshTokenPepper,
		"security.secret_encrypt_key": c.Security.SecretEncryptKey,
	} {
		if isPlaceholderConfigValue(value) {
			return fmt.Errorf("%s must not use an unresolved or placeholder value", name)
		}
	}
	activeMemory := c.EffectiveCodexMemory()
	if activeMemory.Enabled {
		if strings.TrimSpace(activeMemory.LLMProviderType) == "" || strings.TrimSpace(activeMemory.LLMBaseURL) == "" || strings.TrimSpace(activeMemory.LLMAPIKey) == "" || strings.TrimSpace(activeMemory.LLMModel) == "" {
			return fmt.Errorf("codex_memory LLM provider, base URL, API key, and model are required when enabled")
		}
		// Only the deprecated memory_dream alias ever required an embedding
		// provider. Codex file-backed consolidation does not perform vector
		// extraction, so embedding settings are ignored on the active path.
		if !c.CodexMemory.Enabled && c.MemoryDream.Enabled &&
			(strings.TrimSpace(activeMemory.EmbeddingProviderType) == "" || strings.TrimSpace(activeMemory.EmbeddingBaseURL) == "" || strings.TrimSpace(activeMemory.EmbeddingModel) == "") {
			return fmt.Errorf("codex_memory embedding provider, base URL, and model are required when enabled")
		}
	}
	if c.Queue.Backend != "mysql" && c.Queue.Backend != "redis_stream" && c.Queue.Backend != "nats" {
		return fmt.Errorf("queue.backend must be mysql, redis_stream, or nats")
	}
	if c.Queue.Backend == "nats" && c.NATS.URL == "" {
		return fmt.Errorf("nats.url is required when queue.backend is nats")
	}
	if c.ReflectionQueue.Backend != "mysql" && c.ReflectionQueue.Backend != "nats" {
		return fmt.Errorf("reflection_queue.backend must be mysql or nats")
	}
	if c.GitWorkspace.FetchTimeoutSeconds <= 0 || c.GitWorkspace.FetchFreshnessSeconds <= 0 || c.GitWorkspace.GitCommandTimeoutSeconds <= 0 {
		return fmt.Errorf("git_workspace timeout settings must be positive")
	}
	if c.GitWorkspace.MaxOutputBytes <= 0 || c.GitWorkspace.FileReadMaxChars <= 0 || c.GitWorkspace.MaxWorkspacesPerProject <= 0 {
		return fmt.Errorf("git_workspace limits must be positive")
	}
	if c.GitWorkspace.PruneTTLHours <= 0 {
		return fmt.Errorf("git_workspace.prune_ttl_hours must be positive")
	}
	if c.GitWorkspace.Enabled && len(c.GitWorkspace.AllowedRoots) == 0 {
		return fmt.Errorf("git_workspace.allowed_roots is required when enabled")
	}
	for _, root := range c.GitWorkspace.AllowedRoots {
		if !filepath.IsAbs(strings.TrimSpace(root)) {
			return fmt.Errorf("git_workspace.allowed_roots entries must be absolute")
		}
	}
	worktreeDir := strings.TrimSpace(c.GitWorkspace.WorktreeDirName)
	if worktreeDir == "" || filepath.IsAbs(worktreeDir) || filepath.Clean(worktreeDir) != worktreeDir || filepath.Base(worktreeDir) != worktreeDir || worktreeDir == "." || worktreeDir == ".." {
		return fmt.Errorf("git_workspace.worktree_dir_name must be a single relative directory name")
	}
	if c.ReflectionQueue.Backend == "nats" && c.NATS.URL == "" {
		return fmt.Errorf("nats.url is required when reflection_queue.backend is nats")
	}
	if c.ReflectionQueue.AckWaitSeconds <= 0 || c.ReflectionQueue.HeartbeatSeconds <= 0 || c.ReflectionQueue.LeaseSeconds <= 0 {
		return fmt.Errorf("reflection_queue ack, heartbeat, and lease durations must be positive")
	}
	if c.ReflectionQueue.HeartbeatSeconds*2 >= c.ReflectionQueue.AckWaitSeconds {
		return fmt.Errorf("reflection_queue.heartbeat_seconds must be less than half ack_wait_seconds")
	}
	if c.ReflectionQueue.LeaseSeconds < c.ReflectionQueue.AckWaitSeconds {
		return fmt.Errorf("reflection_queue.lease_seconds must be at least ack_wait_seconds")
	}
	if c.ReflectionQueue.Concurrency <= 0 || c.ReflectionQueue.MaxAckPending != c.ReflectionQueue.Concurrency {
		return fmt.Errorf("reflection_queue.max_ack_pending must equal concurrency and both must be positive")
	}
	if c.ReflectionQueue.OutboxBatchSize <= 0 || c.ReflectionQueue.OutboxPollMilliseconds <= 0 || c.ReflectionQueue.StreamReplicas <= 0 {
		return fmt.Errorf("reflection_queue outbox and stream settings must be positive")
	}
	if c.Retrieval.Backend != "elasticsearch" && c.Retrieval.Backend != "milvus" {
		return fmt.Errorf("retrieval.backend must be elasticsearch or milvus")
	}
	if c.Retrieval.Backend == "milvus" && c.Milvus.Address == "" {
		return fmt.Errorf("milvus.address is required when retrieval.backend is milvus")
	}
	if c.Retrieval.Backend == "milvus" && c.Milvus.Dimensions <= 0 {
		return fmt.Errorf("milvus.dimensions must be positive when retrieval.backend is milvus")
	}
	if c.ContextIndex.WorkerEnabled && !c.ContextIndex.Enabled {
		return fmt.Errorf("context_index.enabled must be true when its worker is enabled")
	}
	if c.ContextIndex.BatchSize <= 0 || c.ContextIndex.BatchSize > 500 || c.ContextIndex.PollMilliseconds <= 0 || c.ContextIndex.LeaseSeconds < 10 {
		return fmt.Errorf("context_index worker settings are invalid")
	}
	if c.OCR.Enabled && c.OCR.Endpoint == "" {
		return fmt.Errorf("ocr.endpoint is required when ocr.enabled is true")
	}
	if c.PythonBridge.Enabled {
		if strings.TrimSpace(c.PythonBridge.Target) == "" || strings.TrimSpace(c.PythonBridge.AuthTokenEnv) == "" {
			return fmt.Errorf("python_bridge.target and auth_token_env are required when enabled")
		}
		if c.PythonBridge.ConnectTimeoutSeconds <= 0 || c.PythonBridge.RequestTimeoutSeconds <= 0 {
			return fmt.Errorf("python_bridge timeout settings must be positive")
		}
		if c.PythonBridge.MaxSendBytes <= 0 || c.PythonBridge.MaxReceiveBytes <= 0 || c.PythonBridge.MaxConcurrency <= 0 || c.PythonBridge.MaxConcurrency > 256 {
			return fmt.Errorf("python_bridge limits must be positive")
		}
		for _, method := range c.PythonBridge.AllowedChunkMethods {
			if method != "python:fixed_token" && method != "python:recursive" && method != "python:langchain_recursive" {
				return fmt.Errorf("python_bridge.allowed_chunk_methods contains unsupported method %q", method)
			}
		}
		if c.PythonBridge.DocumentParser != "go" && c.PythonBridge.DocumentParser != "python:langchain_pdf" {
			return fmt.Errorf("python_bridge.document_parser contains unsupported parser %q", c.PythonBridge.DocumentParser)
		}
		if (c.PythonBridge.DocumentParser != "go" || c.PythonBridge.ShadowDocumentParser) && !c.PythonBridge.AllowExperimentalParsing {
			return fmt.Errorf("python_bridge document parsing requires allow_experimental_parsing")
		}
		if c.PythonBridge.MaxDocumentBytes <= 0 || c.PythonBridge.MaxDocumentBytes > c.PythonBridge.MaxSendBytes {
			return fmt.Errorf("python_bridge.max_document_bytes must be positive and no larger than max_send_bytes")
		}
		for _, method := range c.PythonBridge.AllowedParserMethods {
			if method != "python:langchain_pdf" {
				return fmt.Errorf("python_bridge.allowed_parser_methods contains unsupported parser %q", method)
			}
		}
	} else if c.PythonBridge.ShadowEnabled || c.PythonBridge.AllowExperimentalChunking || c.PythonBridge.ShadowDocumentParser || c.PythonBridge.AllowExperimentalParsing || c.PythonBridge.DocumentParser != "go" {
		return fmt.Errorf("python_bridge shadow and experimental features require python_bridge.enabled")
	}
	if c.ResourceCache.TTLSeconds < 1 {
		return fmt.Errorf("resource_cache.ttl_seconds must be positive")
	}
	if c.AgentRuntime.WorkerConcurrency <= 0 || c.AgentRuntime.WorkerConcurrency > 64 || c.AgentRuntime.LeaseSeconds < 10 {
		return fmt.Errorf("agent_runtime worker concurrency or lease duration is invalid")
	}
	if c.AgentRuntime.MemoryReviewMode != "off" && c.AgentRuntime.MemoryReviewMode != "suggest" && c.AgentRuntime.MemoryReviewMode != "auto" {
		return fmt.Errorf("agent_runtime.memory_review_mode must be off, suggest, or auto")
	}
	if c.AgentRuntime.ReviewWorkerConcurrency <= 0 || c.AgentRuntime.ReviewWorkerConcurrency > 16 {
		return fmt.Errorf("agent_runtime.review_worker_concurrency is invalid")
	}
	return nil
}

func isPlaceholderConfigValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "${") || strings.HasPrefix(lower, "change-this-") || strings.HasPrefix(lower, "replace-me-") || lower == "default"
}

func (c SecurityConfig) AccessTokenTTL() time.Duration {
	return time.Duration(c.AccessTokenTTLMinutes) * time.Minute
}

func (c SecurityConfig) RefreshTokenTTL() time.Duration {
	return time.Duration(c.RefreshTokenTTLDays) * 24 * time.Hour
}
