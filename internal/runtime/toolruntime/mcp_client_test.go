package toolruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
			writeMCPHelperResponse(req.ID, map[string]any{"content": params.Arguments.Text})
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
