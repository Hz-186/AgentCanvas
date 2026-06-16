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
	MinIO         MinIOConfig         `yaml:"minio"`
	Elasticsearch ElasticsearchConfig `yaml:"elasticsearch"`
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
	return nil
}

func (c SecurityConfig) AccessTokenTTL() time.Duration {
	return time.Duration(c.AccessTokenTTLMinutes) * time.Minute
}

func (c SecurityConfig) RefreshTokenTTL() time.Duration {
	return time.Duration(c.RefreshTokenTTLDays) * 24 * time.Hour
}
