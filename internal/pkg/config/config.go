package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	App           AppConfig      `json:"app"`
	MySQL         MySQLConfig    `json:"mysql"`
	Redis         RedisConfig    `json:"redis"`
	MinIO         MinIOConfig    `json:"minio"`
	Elasticsearch ElasticConfig  `json:"elasticsearch"`
	Security      SecurityConfig `json:"security"`
}

type AppConfig struct {
}

type MySQLConfig struct {
}

type RedisConfig struct {
}

type MinIOConfig struct {
}

type ElasticConfig struct {
}

type SecurityConfig struct {
}

func Load(path string) (*Config, error) {
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
