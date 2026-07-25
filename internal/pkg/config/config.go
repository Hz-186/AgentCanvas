package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App             AppConfig             `yaml:"app"`
	MySQL           MySQLConfig           `yaml:"mysql"`
	Redis           RedisConfig           `yaml:"redis"`
	Queue           QueueConfig           `yaml:"queue"`
	LLMCache        LLMCacheConfig        `yaml:"llm_cache"`
	ResourceCache   ResourceCacheConfig   `yaml:"resource_cache"`
	MemoryDream     MemoryDreamConfig     `yaml:"memory_dream"`
	WorkingMemory   WorkingMemoryConfig   `yaml:"working_memory"`
	NATS            NATSConfig            `yaml:"nats"`
	ReflectionQueue ReflectionQueueConfig `yaml:"reflection_queue"`
	AgentRuntime    AgentRuntimeConfig    `yaml:"agent_runtime"`
	MinIO           MinIOConfig           `yaml:"minio"`
	Elasticsearch   ElasticsearchConfig   `yaml:"elasticsearch"`
	Milvus          MilvusConfig          `yaml:"milvus"`
	ContextIndex    ContextIndexConfig    `yaml:"context_index"`
	OCR             OCRConfig             `yaml:"ocr"`
	Security        SecurityConfig        `yaml:"security"`
	OAuth           OAuthConfig           `yaml:"oauth"`
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

type AppConfig struct {
	Name    string `yaml:"name"`
	Env     string `yaml:"env"`
	Port    int    `yaml:"port"`
	BaseURL string `yaml:"base_url"`
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
	Enabled             bool    `yaml:"enabled"`
	L1Enabled           bool    `yaml:"l1_enabled"`
	L2Enabled           bool    `yaml:"l2_enabled"`
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
	TTLSeconds          int     `yaml:"ttl_seconds"`
	EmbeddingProviderID int64   `yaml:"embedding_provider_id"`
	EmbeddingModel      string  `yaml:"embedding_model"`
}

type ResourceCacheConfig struct {
	Enabled    bool   `yaml:"enabled"`
	KeyPrefix  string `yaml:"key_prefix"`
	TTLSeconds int    `yaml:"ttl_seconds"`
}

type MemoryDreamConfig struct {
	Enabled               bool   `yaml:"enabled"`
	TriggerEveryNTurns    int    `yaml:"trigger_every_n_turns"`
	IdleTimeoutSeconds    int    `yaml:"idle_timeout_seconds"`
	LLMProviderType       string `yaml:"llm_provider_type"`
	LLMBaseURL            string `yaml:"llm_base_url"`
	LLMAPIKey             string `yaml:"llm_api_key"`
	LLMModel              string `yaml:"llm_model"`
	EmbeddingProviderType string `yaml:"embedding_provider_type"`
	EmbeddingBaseURL      string `yaml:"embedding_base_url"`
	EmbeddingAPIKey       string `yaml:"embedding_api_key"`
	EmbeddingModel        string `yaml:"embedding_model"`
}

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

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.setDefaults()
	return &cfg, cfg.Validate()
}

func (c *Config) setDefaults() {
	if c.App.Name == "" {
		c.App.Name = "agentcanvas"
	}
	if c.App.Env == "" {
		c.App.Env = "local"
	}
	if c.App.Port == 0 {
		c.App.Port = 8080
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
	if !c.LLMCache.L1Enabled && !c.LLMCache.L2Enabled && !c.LLMCache.Enabled {
		c.LLMCache.Enabled = false
	}
	if c.LLMCache.Enabled {
		if !c.LLMCache.L1Enabled && !c.LLMCache.L2Enabled {
			c.LLMCache.L1Enabled = true
		}
	}
	if c.LLMCache.SimilarityThreshold == 0 {
		c.LLMCache.SimilarityThreshold = 0.96
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
	if c.MemoryDream.TriggerEveryNTurns == 0 {
		c.MemoryDream.TriggerEveryNTurns = 5
	}
	if c.MemoryDream.IdleTimeoutSeconds == 0 {
		c.MemoryDream.IdleTimeoutSeconds = 180
	}
	if c.WorkingMemory.TTLSeconds == 0 {
		c.WorkingMemory.TTLSeconds = 86400
	}
	if c.WorkingMemory.LockTTLMS == 0 {
		c.WorkingMemory.LockTTLMS = 5000
	}
	if c.WorkingMemory.LockWaitMS == 0 {
		c.WorkingMemory.LockWaitMS = 500
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
		c.Milvus.Collection = "agentcanvas_chunks"
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
	if c.Queue.Backend != "mysql" && c.Queue.Backend != "redis_stream" && c.Queue.Backend != "nats" {
		return fmt.Errorf("queue.backend must be mysql, redis_stream, or nats")
	}
	if c.Queue.Backend == "nats" && c.NATS.URL == "" {
		return fmt.Errorf("nats.url is required when queue.backend is nats")
	}
	if c.ReflectionQueue.Backend != "mysql" && c.ReflectionQueue.Backend != "nats" {
		return fmt.Errorf("reflection_queue.backend must be mysql or nats")
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
	if c.Milvus.Enabled && c.Milvus.Address == "" {
		return fmt.Errorf("milvus.address is required when milvus.enabled is true")
	}
	if c.ContextIndex.Enabled && !c.Milvus.Enabled {
		return fmt.Errorf("milvus.enabled must be true when context_index.enabled is true")
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

func (c SecurityConfig) AccessTokenTTL() time.Duration {
	return time.Duration(c.AccessTokenTTLMinutes) * time.Minute
}

func (c SecurityConfig) RefreshTokenTTL() time.Duration {
	return time.Duration(c.RefreshTokenTTLDays) * 24 * time.Hour
}
