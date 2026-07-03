package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/engine"

	"gorm.io/gorm"
)

func TestMCPToolNodeCallsStdioServer(t *testing.T) {
	node := MCPToolNode{Servers: fakeMCPServerRepo{server: &tool.MCPServer{
		ID:        10,
		OwnerID:   1,
		Name:      "stdio",
		Transport: tool.MCPTransportStdio,
		Command:   os.Args[0],
		ArgsJSON:  mustRawJSON([]string{"-test.run=TestMCPToolNodeHelperProcess", "--"}),
		EnvJSON:   mustRawJSON(map[string]string{"MCP_NODE_HELPER": "1"}),
		Status:    tool.MCPStatusActive,
	}}}
	output, err := node.Run(context.Background(), &engine.RunContext{OwnerID: 1}, nil, json.RawMessage(`{"server_id":10,"tool_name":"echo","input":{"text":"hello"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(output["content_json"].(json.RawMessage)) != `{"content":"hello"}` {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestMCPToolNodeRejectsDisabledServer(t *testing.T) {
	node := MCPToolNode{Servers: fakeMCPServerRepo{server: &tool.MCPServer{
		ID:        10,
		OwnerID:   1,
		Name:      "disabled",
		Transport: tool.MCPTransportSSE,
		Status:    tool.MCPStatusDisabled,
	}}}
	if _, err := node.Run(context.Background(), &engine.RunContext{OwnerID: 1}, nil, json.RawMessage(`{"server_id":10,"tool_name":"echo"}`)); err == nil {
		t.Fatal("expected disabled server to be rejected")
	}
}

func TestMCPToolNodeValidate(t *testing.T) {
	node := MCPToolNode{}
	if err := node.Validate(json.RawMessage(`{"server_id":0,"tool_name":"echo"}`)); err == nil {
		t.Fatal("expected missing server_id error")
	}
	if err := node.Validate(json.RawMessage(`{"server_id":1,"tool_name":"echo","input":{"x":1}}`)); err != nil {
		t.Fatalf("expected valid config: %v", err)
	}
}

func TestAgentNodeLoadToolsIncludesMCPServerTools(t *testing.T) {
	agent := AgentNode{MCPServers: fakeMCPServerRepo{server: &tool.MCPServer{
		ID:        10,
		OwnerID:   1,
		Name:      "stdio",
		Transport: tool.MCPTransportStdio,
		Command:   os.Args[0],
		ArgsJSON:  mustRawJSON([]string{"-test.run=TestMCPToolNodeHelperProcess", "--"}),
		EnvJSON:   mustRawJSON(map[string]string{"MCP_NODE_HELPER": "1"}),
		Status:    tool.MCPStatusActive,
	}}}
	tools, err := agent.loadTools(context.Background(), 1, agentRuntimeConfig{MCPServerIDs: []int64{10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name() != "request_human_approval" || tools[1].Name() != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestMCPToolNodeHelperProcess(t *testing.T) {
	if os.Getenv("MCP_NODE_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.ID == 0 {
			continue
		}
		switch req.Method {
		case "initialize":
			writeMCPNodeHelperResponse(req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "tools/list":
			writeMCPNodeHelperResponse(req.ID, map[string]any{"tools": []map[string]any{{"name": "echo", "description": "echo text", "parameters": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}}}})
		case "tools/call":
			var params struct {
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			writeMCPNodeHelperResponse(req.ID, map[string]any{"content": params.Arguments.Text})
		default:
			writeMCPNodeHelperResponse(req.ID, map[string]any{})
		}
	}
	os.Exit(0)
}

func writeMCPNodeHelperResponse(id int64, result any) {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Println(string(data))
}

type fakeMCPServerRepo struct {
	server *tool.MCPServer
}

func (r fakeMCPServerRepo) CreateServer(context.Context, *tool.MCPServer) error { return nil }
func (r fakeMCPServerRepo) FindServerByID(_ context.Context, ownerID, id int64) (*tool.MCPServer, error) {
	if r.server != nil && r.server.OwnerID == ownerID && r.server.ID == id {
		clone := *r.server
		return &clone, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (r fakeMCPServerRepo) ListServers(context.Context, int64) ([]tool.MCPServer, error) {
	return nil, nil
}
func (r fakeMCPServerRepo) UpdateServer(context.Context, *tool.MCPServer) error { return nil }
func (r fakeMCPServerRepo) DeleteServer(context.Context, int64, int64) error    { return nil }
func (r fakeMCPServerRepo) ReplaceToolCache(context.Context, int64, int64, []tool.MCPToolCache) error {
	return nil
}
func (r fakeMCPServerRepo) ListToolCache(context.Context, int64, int64) ([]tool.MCPToolCache, error) {
	return nil, nil
}

func mustRawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
