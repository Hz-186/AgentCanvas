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
	MCPDisabled = false
	MCPEnabled  = true
)

type MCPServer struct {
	domain.SoftDeleteModel
	Name              string          `json:"name" gorm:"column:name"`
	Transport         string          `json:"transport" gorm:"column:transport"`
	EndpointURL       string          `json:"endpoint_url" gorm:"column:endpoint_url"` // standard MCP endpoint, e.g. https://mcp.example.com/mcp
	Command           string          `json:"command" gorm:"column:command"`           // "npx"
	ArgsJSON          json.RawMessage `json:"args_json" gorm:"column:args_json"`
	EnvJSON           json.RawMessage `json:"-" gorm:"column:env_json"` // write-only credentials
	Enabled           bool            `json:"enabled" gorm:"column:enabled"`
	DiscoveryError    string          `json:"discovery_error" gorm:"column:discovery_error"`
	ToolsDiscoveredAt *time.Time      `json:"tools_discovered_at" gorm:"column:tools_discovered_at"`
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

type MCPToolCacheEntry struct {
	domain.ImmutableModel
	MCPServerID     int64           `json:"mcp_server_id" gorm:"column:mcp_server_id"`
	ToolName        string          `json:"tool_name" gorm:"column:tool_name"`
	Description     string          `json:"description" gorm:"column:description"`
	InputSchemaJSON json.RawMessage `json:"input_schema_json" gorm:"column:input_schema_json"` // JSON Schema
}

func (MCPToolCacheEntry) TableName() string { return "mcp_tool_cache" }
