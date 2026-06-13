package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	App           AppConfig           `json:"app"`
	MySQL         MySQLConfig         `json:"mysql"`
	Redis         RedisConfig         `json:"redis"`
	MinIO         MinIOConfig         `json:"minio"`
	Elasticsearch ElasticsearchConfig `json:"elasticsearch"`
	Security      SecurityConfig      `json:"security"`
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
	JWTSecret          string `yaml:"jwt_secret"`
	RefreshTokenPepper string `yaml:"refresh_token_pepper"`
	SecretEncryptKey   string `yaml:"secret_encrypt_key"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
