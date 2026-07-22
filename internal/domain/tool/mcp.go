package tool

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/domain"

	"gorm.io/gorm"
)

const (
	MCPTransportStreamableHTTP = "streamable_http"
	MCPTransportStdio          = "stdio"
)

const (
	MCPStatusDisabled = domain.StatusDisabled
	MCPStatusActive   = domain.StatusActive
)

type MCPServer struct {
	ID           int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64           `json:"owner_id" gorm:"column:owner_id"`
	Name         string          `json:"name" gorm:"column:name"`
	Transport    string          `json:"transport" gorm:"column:transport"`
	EndpointURL  string          `json:"endpoint_url" gorm:"column:endpoint_url"` // standard MCP endpoint, e.g. https://mcp.example.com/mcp
	Command      string          `json:"command" gorm:"column:command"`           // "npx"
	ArgsJSON     json.RawMessage `json:"args_json" gorm:"column:args_json"`
	EnvJSON      json.RawMessage `json:"env_json" gorm:"column:env_json"` // supplyment / API key
	Status       int             `json:"status" gorm:"column:status"`
	LastError    string          `json:"last_error" gorm:"column:last_error"`
	DiscoveredAt *time.Time      `json:"discovered_at" gorm:"column:discovered_at"`
	CreatedAt    time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt    time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt    *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (MCPServer) TableName() string { return "mcp_servers" }

func (s *MCPServer) ArgsSlice() []string {
	var args []string
	_ = json.Unmarshal(s.ArgsJSON, &args)
	return args
}

func (s *MCPServer) EnvMap() map[string]string {
	env := map[string]string{}
	_ = json.Unmarshal(s.EnvJSON, &env)
	return env
}

func (s *MCPServer) BeforeCreate(tx *gorm.DB) error {
	return s.normalizeJSON()
}

func (s *MCPServer) BeforeUpdate(tx *gorm.DB) error {
	return s.normalizeJSON()
}

func (s *MCPServer) normalizeJSON() error {
	if len(s.ArgsJSON) == 0 || string(s.ArgsJSON) == "null" {
		s.ArgsJSON = json.RawMessage("[]")
	}
	if len(s.EnvJSON) == 0 || string(s.EnvJSON) == "null" {
		s.EnvJSON = json.RawMessage("{}")
	}
	return nil
}

type MCPToolCache struct {
	ID             int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID        int64           `json:"owner_id" gorm:"column:owner_id"`
	ServerID       int64           `json:"server_id" gorm:"column:server_id"` // which one
	ToolName       string          `json:"tool_name" gorm:"column:tool_name"`
	Description    string          `json:"description" gorm:"column:description"`
	ParametersJSON json.RawMessage `json:"parameters_json" gorm:"column:parameters_json"` // (JSON Schema)
	SchemaHash     string          `json:"schema_hash" gorm:"column:schema_hash"`
	CachedAt       time.Time       `json:"cached_at" gorm:"column:cached_at"`
	CreatedAt      time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (MCPToolCache) TableName() string { return "mcp_tool_cache" }
