package toolruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMCPStdioClientDiscoversAndCallsTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewMCPStdioClient("local-test", os.Args[0], []string{"-test.run=TestMCPStdioHelperProcess", "--"}, map[string]string{
		"MCP_STDIO_HELPER": "1",
	})
	tools, err := client.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["content"] != "hello" {
		t.Fatalf("unexpected output: %+v", output)
	}
	second, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"again"}`))
	if err != nil {
		t.Fatal(err)
	}
	var secondOutput map[string]any
	_ = json.Unmarshal(second.ContentJSON, &secondOutput)
	if output["pid"] != secondOutput["pid"] {
		t.Fatalf("stdio calls did not reuse one MCP session: first=%v second=%v", output["pid"], secondOutput["pid"])
	}
}

func TestMCPToolRuntimeMetadataIncludesHTTPHost(t *testing.T) {
	client := NewMCPClient("remote", "https://mcp.example.com/sse")
	tool := NewMCPToolRuntime(MCPToolDef{Name: "search"}, client)
	metadata := MetadataOf(tool)
	if len(metadata.AllowedHosts) != 1 || metadata.AllowedHosts[0] != "mcp.example.com" {
		t.Fatalf("expected SSE host in metadata, got %+v", metadata)
	}
}

func TestMCPStreamableHTTPInitializesListsAndCalls(t *testing.T) {
	initialized := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mcpJSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch request.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("MCP-Session-Id", "session-1")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "test", "version": "1"}}})
		case "notifications/initialized":
			initialized = true
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized || r.Header.Get("MCP-Session-Id") != "session-1" || r.Header.Get("MCP-Protocol-Version") != mcpProtocolVersion {
				t.Errorf("missing negotiated MCP headers: %+v", r.Header)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":\"echo\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", request.ID)
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": "ok"}})
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := NewMCPClient("remote", server.URL)
	tools, err := client.Discover(context.Background())
	if err != nil || len(tools) != 1 || string(tools[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("unexpected discovery: tools=%+v err=%v", tools, err)
	}
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(result.ContentJSON), "ok") {
		t.Fatalf("unexpected call result: result=%+v err=%v", result, err)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("MCP_STDIO_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.ID == 0 {
			continue
		}
		switch req.Method {
		case "initialize":
			writeMCPHelperResponse(req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "helper", "version": "test"},
			})
		case "tools/list":
			writeMCPHelperResponse(req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "echo text",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required":   []string{"text"},
					},
				}},
			})
		case "tools/call":
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			writeMCPHelperResponse(req.ID, map[string]any{"content": params.Arguments.Text, "pid": os.Getpid()})
		default:
			writeMCPHelperError(req.ID, -32601, "method not found")
		}
	}
	os.Exit(0)
}

func writeMCPHelperResponse(id int64, result any) {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Println(string(data))
}

func writeMCPHelperError(id int64, code int, message string) {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	fmt.Println(string(data))
}
