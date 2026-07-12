package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/infrastructure/llm"
	runtimeagent "agentcanvas/internal/runtime/agent"
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
	tools, err := agent.loadTools(context.Background(), 1, agentRuntimeConfig{MCPServerIDs: []int64{10}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name() != "request_human_approval" || tools[1].Name() != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestAgentNodeLoadMCPToolsUsesCachedDefinitions(t *testing.T) {
	agent := AgentNode{MCPServers: fakeMCPServerRepo{
		server: &tool.MCPServer{
			ID:        10,
			OwnerID:   1,
			Name:      "cached",
			Transport: tool.MCPTransportStdio,
			Command:   "missing-mcp-helper",
			Status:    tool.MCPStatusActive,
		},
		cache: []tool.MCPToolCache{{
			OwnerID:        1,
			ServerID:       10,
			ToolName:       "cached_echo",
			Description:    "cached tool",
			ParametersJSON: json.RawMessage(`{"type":"object"}`),
		}},
	}}
	tools, err := agent.loadTools(context.Background(), 1, agentRuntimeConfig{MCPServerIDs: []int64{10}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[1].Name() != "cached_echo" {
		t.Fatalf("expected cached MCP tool without live discover, got %+v", tools)
	}
}

func TestAgentNodeLoadToolsExposesDynamicDelegationAsCallAgent(t *testing.T) {
	agent := AgentNode{WorkflowCaller: &fakeNodeAgentCaller{}}
	tools, err := agent.loadTools(context.Background(), 1, agentRuntimeConfig{CallWorkflowIDs: []int64{10}, MaxWorkflowCallDepth: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name() != "request_human_approval" || tools[1].Name() != "call_agent" {
		t.Fatalf("expected agent_loop delegation tool to be call_agent, got %+v", tools)
	}
}

func TestAgentLoopNodePlanExecuteReturnsPlanTrace(t *testing.T) {
	client := &fakeNodeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: "assistant", Content: `{"steps":[{"number":1,"description":"inspect input"},{"number":2,"description":"write answer"}]}`}},
		{Message: llm.ChatMessage{Role: "assistant", Content: "planned answer"}},
	}}
	node := AgentLoopNode{AgentNode: AgentNode{LLM: client, Providers: fakeProviderLoader{}}}
	output, err := node.Run(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2, RunID: 3, CurrentNodeID: "agent"}, engine.NodeInput{"query": "hello"}, json.RawMessage(`{
		"mode":"plan_execute",
		"provider_id":1,
		"model":"test-model",
		"task_template":"{{sys.query}}",
		"max_iterations":3,
		"max_tool_calls":2,
		"return_intermediate_steps":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if output["stop_reason"] != "plan_completed" {
		t.Fatalf("expected plan_completed, got %+v", output)
	}
	if output["plan"] == nil {
		t.Fatalf("expected plan in output: %+v", output)
	}
	steps, ok := output["steps"].([]runtimeagent.RunStep)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected compacted steps in output: %+v", output["steps"])
	}
}

func TestAgentLoopNodeRepairsStructuredOutputWithReflection(t *testing.T) {
	client := &fakeNodeToolClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: "assistant", Content: "not json"}},
		{Message: llm.ChatMessage{Role: "assistant", Content: `{"answer":"fixed"}`}},
	}}
	node := AgentLoopNode{AgentNode: AgentNode{LLM: client, Providers: fakeProviderLoader{}}}
	output, err := node.Run(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2, RunID: 3, CurrentNodeID: "agent"}, engine.NodeInput{"query": "hello"}, json.RawMessage(`{
		"provider_id":1,
		"model":"test-model",
		"task_template":"{{sys.query}}",
		"max_iterations":2,
		"max_tool_calls":2,
		"return_intermediate_steps":true,
		"output_schema_json":{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if output["final_answer"] != `{"answer":"fixed"}` {
		t.Fatalf("expected repaired final answer, got %+v", output)
	}
	structured, ok := output["structured_output"].(map[string]any)
	if !ok || structured["answer"] != "fixed" {
		t.Fatalf("expected structured output, got %+v", output["structured_output"])
	}
	steps, ok := output["steps"].([]runtimeagent.RunStep)
	if !ok {
		t.Fatalf("expected runtime steps, got %T", output["steps"])
	}
	foundReflection := false
	for _, step := range steps {
		if step.Type == runtimeagent.StepTypeReflection {
			foundReflection = true
		}
	}
	if !foundReflection {
		t.Fatalf("expected reflection step, got %+v", steps)
	}
}

func TestAgentLoopNodeParsesNestedPolicyConfig(t *testing.T) {
	cfg, err := parseAgentNodeConfig(json.RawMessage(`{
		"model":{"provider_id":1,"model":"test-model"},
		"task_template":"{{sys.query}}",
		"policy":{
			"require_approval_for_risk":["high"],
			"max_tool_timeout_ms":1500,
			"max_tool_output_bytes":2048,
			"allowed_hosts":["api.example.com"]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderID != 1 || cfg.Model != "test-model" || cfg.MaxToolTimeoutMS != 1500 || cfg.MaxToolOutputBytes != 2048 {
		t.Fatalf("nested config was not parsed: %+v", cfg)
	}
	if len(cfg.RequireApprovalForRisk) != 1 || cfg.RequireApprovalForRisk[0] != "high" {
		t.Fatalf("approval policy was not parsed: %+v", cfg.RequireApprovalForRisk)
	}
	if len(cfg.AllowedHosts) != 1 || cfg.AllowedHosts[0] != "api.example.com" {
		t.Fatalf("allowed hosts were not parsed: %+v", cfg.AllowedHosts)
	}
	if err := (AgentLoopNode{}).Validate(json.RawMessage(`{
		"model":{"provider_id":1,"model":"test-model"},
		"policy":{"max_tool_output_bytes":2097153}
	}`)); err == nil {
		t.Fatal("expected oversized policy output limit to fail validation")
	}
}

func TestAgentLoopNodeResumePausesWhenCheckpointToolRegistryHashChanged(t *testing.T) {
	client := &fakeNodeToolClient{}
	node := AgentLoopNode{AgentNode: AgentNode{LLM: client, Providers: fakeProviderLoader{}}}
	checkpoint := &runtimeagent.Checkpoint{
		MessagesSummary: "user: paused",
		Metadata: map[string]any{
			"node_id":            "agent",
			"tool_registry_hash": "stale-registry-hash",
		},
	}
	output, err := node.Resume(
		context.Background(),
		&engine.RunContext{OwnerID: 1, WorkflowID: 2, RunID: 3, CurrentNodeID: "agent", Input: map[string]any{"query": "resume"}},
		engine.NodeInput{"query": "resume"},
		json.RawMessage(`{"provider_id":1,"model":"test-model","task_template":"{{sys.query}}","max_iterations":3,"max_tool_calls":2}`),
		AgentResumeOptions{Checkpoint: checkpoint, Approved: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if output["stop_reason"] != runtimeagent.StopReasonPaused {
		t.Fatalf("expected paused output, got %+v", output)
	}
	if len(client.requests) != 0 {
		t.Fatalf("LLM should not be called when checkpoint hash mismatches, got %d requests", len(client.requests))
	}
	pausedCheckpoint, ok := output["checkpoint"].(*runtimeagent.Checkpoint)
	if !ok || pausedCheckpoint.Metadata["resume_blocked_reason"] == "" {
		t.Fatalf("expected checkpoint with blocked reason, got %+v", output["checkpoint"])
	}
}

func TestAgentNodeApplyProfileDefaultsInheritsPolicyAndContext(t *testing.T) {
	agent := AgentNode{Profiles: fakeAgentProfileLoader{profile: &workflow.Profile{
		OwnerID:           1,
		WorkflowID:        2,
		Mode:              "reflect",
		ToolPolicyJSON:    json.RawMessage(`{"require_approval_for_risk":["medium"],"max_tool_timeout_ms":1500,"max_tool_output_bytes":4096,"allowed_hosts":["api.example.com"]}`),
		MemoryPolicyJSON:  json.RawMessage(`{"enabled":true}`),
		ContextPolicyJSON: json.RawMessage(`{"max_input_tokens":12000}`),
	}}}
	cfg, err := agent.applyProfileDefaults(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2}, agentRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "react" || !cfg.ReflectionEnabled || cfg.MaxInputChars != 48000 || cfg.MaxToolTimeoutMS != 1500 || cfg.MaxToolOutputBytes != 4096 {
		t.Fatalf("profile defaults were not inherited: %+v", cfg)
	}
	if len(cfg.RequireApprovalForRisk) != 1 || cfg.RequireApprovalForRisk[0] != "medium" {
		t.Fatalf("profile approval policy not inherited: %+v", cfg.RequireApprovalForRisk)
	}
	if len(cfg.AllowedHosts) != 1 || cfg.AllowedHosts[0] != "api.example.com" {
		t.Fatalf("profile allowed hosts not inherited: %+v", cfg.AllowedHosts)
	}
	if !cfg.MemoryEnabled {
		t.Fatalf("profile memory policy not inherited: %+v", cfg)
	}
}

func TestAgentNodePolicyJSONOverridesProfileDefaults(t *testing.T) {
	agent := AgentNode{Profiles: fakeAgentProfileLoader{profile: &workflow.Profile{
		OwnerID:           1,
		WorkflowID:        2,
		MemoryEnabled:     true,
		MemoryPolicyJSON:  json.RawMessage(`{"enabled":true}`),
		ContextPolicyJSON: json.RawMessage(`{"max_input_tokens":12000}`),
		ToolPolicyJSON:    json.RawMessage(`{"require_approval_for_risk":["high"],"max_tool_timeout_ms":30000,"max_tool_output_bytes":8192,"allowed_hosts":["profile.example.com"]}`),
	}}}
	cfg, err := agent.applyProfileDefaults(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2}, agentRuntimeConfig{
		ToolPolicyJSON:    json.RawMessage(`{"require_approval_for_risk":[],"max_tool_timeout_ms":1500,"max_tool_output_bytes":4096,"allowed_hosts":["node.example.com"]}`),
		MemoryPolicyJSON:  json.RawMessage(`{"enabled":false}`),
		ContextPolicyJSON: json.RawMessage(`{"max_input_tokens":1000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg = applyNodeMemoryPolicy(cfg, cfg.MemoryPolicyJSON)
	cfg = applyNodeContextPolicy(cfg, cfg.ContextPolicyJSON)
	cfg = applyNodeToolPolicy(cfg, cfg.ToolPolicyJSON)
	if cfg.MemoryEnabled {
		t.Fatalf("node memory policy should override profile default: %+v", cfg)
	}
	if cfg.MaxInputChars != 4000 {
		t.Fatalf("node context policy should override profile default, got %+v", cfg)
	}
	if len(cfg.RequireApprovalForRisk) != 0 || cfg.MaxToolTimeoutMS != 1500 || cfg.MaxToolOutputBytes != 4096 || len(cfg.AllowedHosts) != 1 || cfg.AllowedHosts[0] != "node.example.com" {
		t.Fatalf("node tool policy should override profile default, got %+v", cfg)
	}
}

func TestParseAgentNodeConfigAcceptsNestedPolicyJSON(t *testing.T) {
	cfg, err := parseAgentNodeConfig(json.RawMessage(`{
		"model":{"provider_id":1,"model":"test"},
		"task_template":"{{sys.query}}",
		"memory":{"policy":{"enabled":false}},
		"context":{"max_input_tokens":12000,"policy":{"max_input_tokens":1000}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.MemoryPolicyJSON) != `{"enabled":false}` || string(cfg.ContextPolicyJSON) != `{"max_input_tokens":1000}` {
		t.Fatalf("expected nested policy JSON to be parsed, got %+v", cfg)
	}
	if cfg.MaxInputChars != 48000 {
		t.Fatalf("expected direct context budget to be preserved before policy override, got %+v", cfg)
	}
}

func TestAgentNodeApplyProfilePlanningOverridesDefaultReactMode(t *testing.T) {
	agent := AgentNode{Profiles: fakeAgentProfileLoader{profile: &workflow.Profile{
		OwnerID:         1,
		WorkflowID:      2,
		Mode:            "react",
		PlanningEnabled: true,
	}}}
	cfg, err := agent.applyProfileDefaults(context.Background(), &engine.RunContext{OwnerID: 1, WorkflowID: 2}, agentRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "plan_execute" {
		t.Fatalf("expected planning-enabled profile to default to plan_execute, got %+v", cfg)
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
	cache  []tool.MCPToolCache
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
	return append([]tool.MCPToolCache(nil), r.cache...), nil
}

type fakeProviderLoader struct{}

func (fakeProviderLoader) LoadChatProviderConfig(context.Context, int64, int64, string) (*LoadedProvider, error) {
	return &LoadedProvider{ProviderID: 1, Model: "test-model", Config: llm.ChatProviderConfig{}}, nil
}

type fakeAgentProfileLoader struct {
	profile *workflow.Profile
}

func (l fakeAgentProfileLoader) GetWorkflowProfile(context.Context, int64, int64) (*workflow.Profile, error) {
	return l.profile, nil
}

type fakeNodeToolClient struct {
	responses []llm.ToolChatResponse
	requests  []llm.ToolChatRequest
}

func (c *fakeNodeToolClient) ChatWithTools(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return &llm.ToolChatResponse{Message: llm.ChatMessage{Role: "assistant", Content: "done"}}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return &resp, nil
}

func mustRawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
