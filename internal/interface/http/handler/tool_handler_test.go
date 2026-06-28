package handler

import (
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/tool"
)

func TestNormalizeMCPServerRequestSSE(t *testing.T) {
	item := &tool.MCPServer{Name: " Local MCP ", Transport: tool.MCPTransportSSE, EndpointURL: " http://localhost:3333 "}
	if err := normalizeMCPServerRequest(item); err != nil {
		t.Fatal(err)
	}
	if item.Name != "Local MCP" || item.EndpointURL != "http://localhost:3333" || string(item.ArgsJSON) != "[]" || string(item.EnvJSON) != "{}" {
		t.Fatalf("unexpected server: %+v args=%s env=%s", item, item.ArgsJSON, item.EnvJSON)
	}
}

func TestNormalizeMCPServerRequestStdio(t *testing.T) {
	item := &tool.MCPServer{Name: "Local", Transport: tool.MCPTransportStdio, Command: " node ", ArgsJSON: json.RawMessage(`["server.js"]`)}
	if err := normalizeMCPServerRequest(item); err != nil {
		t.Fatal(err)
	}
	if item.Command != "node" || string(item.ArgsJSON) != `["server.js"]` {
		t.Fatalf("unexpected server: %+v", item)
	}
}

func TestNormalizeMCPServerRequestRejectsMissingEndpoint(t *testing.T) {
	item := &tool.MCPServer{Name: "Bad", Transport: tool.MCPTransportSSE}
	if err := normalizeMCPServerRequest(item); err == nil {
		t.Fatal("expected missing endpoint error")
	}
}
