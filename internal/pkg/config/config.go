package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App           AppConfig           `yaml:"app"`
	MySQL         MySQLConfig         `yaml:"mysql"`
	Redis         RedisConfig         `yaml:"redis"`
	Queue         QueueConfig         `yaml:"queue"`
	LLMCache      LLMCacheConfig      `yaml:"llm_cache"`
	MemoryDream   MemoryDreamConfig   `yaml:"memory_dream"`
	NATS          NATSConfig          `yaml:"nats"`
	MinIO         MinIOConfig         `yaml:"minio"`
	Elasticsearch ElasticsearchConfig `yaml:"elasticsearch"`
	Milvus        MilvusConfig        `yaml:"milvus"`
	OCR           OCRConfig           `yaml:"ocr"`
	Security      SecurityConfig      `yaml:"security"`
	OAuth         OAuthConfig         `yaml:"oauth"`
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

type MemoryDreamConfig struct {
	Enabled            bool   `yaml:"enabled"`
	TriggerEveryNTurns int    `yaml:"trigger_every_n_turns"`
	IdleTimeoutSeconds int    `yaml:"idle_timeout_seconds"`
	LLMProviderType    string `yaml:"llm_provider_type"`
	LLMBaseURL         string `yaml:"llm_base_url"`
	LLMAPIKey          string `yaml:"llm_api_key"`
	LLMModel           string `yaml:"llm_model"`
}

type NATSConfig struct {
	URL            string `yaml:"url"`
	Stream         string `yaml:"stream"`
	Subject        string `yaml:"subject"`
	Consumer       string `yaml:"consumer"`
	Durable        string `yaml:"durable"`
	AckWaitSeconds int    `yaml:"ack_wait_seconds"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type ElasticsearchConfig struct {
	Addresses  []string `yaml:"addresses"`
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password"`
	ChunkIndex string   `yaml:"chunk_index"`
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
	if c.MemoryDream.TriggerEveryNTurns == 0 {
		c.MemoryDream.TriggerEveryNTurns = 5
	}
	if c.MemoryDream.IdleTimeoutSeconds == 0 {
		c.MemoryDream.IdleTimeoutSeconds = 180
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
	if c.Milvus.Enabled && c.Milvus.Address == "" {
		return fmt.Errorf("milvus.address is required when milvus.enabled is true")
	}
	if c.OCR.Enabled && c.OCR.Endpoint == "" {
		return fmt.Errorf("ocr.endpoint is required when ocr.enabled is true")
	}
	return nil
}

func (c SecurityConfig) AccessTokenTTL() time.Duration {
	return time.Duration(c.AccessTokenTTLMinutes) * time.Minute
}

func (c SecurityConfig) RefreshTokenTTL() time.Duration {
	return time.Duration(c.RefreshTokenTTLDays) * 24 * time.Hour
}
