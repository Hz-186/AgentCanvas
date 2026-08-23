package tool

import (
	"encoding/json"
	"testing"
)

func TestMCPServerNormalizeJSON(t *testing.T) {
	server := &MCPServer{ArgsJSON: json.RawMessage("null"), EnvJSON: nil}
	if err := server.normalizeJSON(); err != nil {
		t.Fatal(err)
	}
	if string(server.ArgsJSON) != "[]" || string(server.EnvJSON) != "{}" {
		t.Fatalf("unexpected normalized json: args=%s env=%s", server.ArgsJSON, server.EnvJSON)
	}
}

func TestMCPServerArgsAndEnv(t *testing.T) {
	server := &MCPServer{
		ArgsJSON: json.RawMessage(`["--debug","serve"]`),
		EnvJSON:  json.RawMessage(`{"TOKEN":"x"}`),
	}
	args := server.ArgsSlice()
	env := server.EnvMap()
	if len(args) != 2 || args[0] != "--debug" || env["TOKEN"] != "x" {
		t.Fatalf("unexpected args/env: %+v %+v", args, env)
	}
}

func TestMCPServerEnvironmentIsWriteOnly(t *testing.T) {
	data, err := json.Marshal(&MCPServer{EnvJSON: json.RawMessage(`{"TOKEN":"secret"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["env_json"]; ok {
		t.Fatalf("MCP response exposed environment: %s", data)
	}
}

func TestMCPTableNames(t *testing.T) {
	if (MCPServer{}).TableName() != "mcp_servers" {
		t.Fatal("unexpected mcp server table")
	}
	if (MCPToolCacheEntry{}).TableName() != "mcp_tool_cache" {
		t.Fatal("unexpected mcp tool cache table")
	}
}
