package engine_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/toolruntime"
)

func TestExecutorRunsLinearFlow(t *testing.T) {
	executor := engine.NewExecutor([]engine.Node{runtimenode.BeginNode{}, runtimenode.PromptNode{}, runtimenode.MessageNode{}})
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "prompt_1", Type: "prompt", Config: json.RawMessage(`{"template":"问题：{{sys.query}}"}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{"content":"{{prompt_1.prompt}}"}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "prompt_1"}, {From: "prompt_1", To: "message_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "Agent Flow 如何执行？"}}
	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "问题：Agent Flow 如何执行？" {
		t.Fatalf("content = %v", output["content"])
	}
	if rc.NodeOutputs["prompt_1"]["prompt"] != "问题：Agent Flow 如何执行？" {
		t.Fatalf("prompt output = %v", rc.NodeOutputs["prompt_1"])
	}
}

func TestExecutorRunsFullPhase4Flow(t *testing.T) {
	retriever := &fakeRetriever{}
	chat := &fakeChatClient{}
	messages := &fakeMessageWriter{}
	events := &collectingEmitter{}
	executor := engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{Retriever: retriever, LLM: chat, Providers: fakeProviderLoader{}, Messages: messages}))
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_phase4",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "retrieval_1", Type: "knowledge_retrieval", Config: json.RawMessage(`{"kb_ids":[10],"top_k":3,"mode":"keyword","query":"{{sys.query}}"}`)},
			{ID: "prompt_1", Type: "prompt", Config: json.RawMessage(`{"template":"上下文：{{retrieval_1.context}}\n问题：{{sys.query}}"}`)},
			{ID: "llm_1", Type: "llm", Config: json.RawMessage(`{"provider_id":7,"model":"demo-chat","stream":false}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{"content":"{{llm_1.content}}","with_citation":true}`)},
		},
		Edges: []flow.Edge{
			{From: "begin_1", To: "retrieval_1"},
			{From: "retrieval_1", To: "prompt_1"},
			{From: "prompt_1", To: "llm_1"},
			{From: "llm_1", To: "message_1"},
		},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "Agent Flow Runtime 如何执行？"}, Events: events}

	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "Phase 4 已完成" {
		t.Fatalf("content = %v", output["content"])
	}
	if retriever.request.Query != "Agent Flow Runtime 如何执行？" || retriever.request.TopK != 3 {
		t.Fatalf("retrieval request = %+v", retriever.request)
	}
	if !strings.Contains(chat.request.Messages[0].Content, "Runtime 按 DAG 顺序执行节点") || !strings.Contains(chat.request.Messages[0].Content, "Agent Flow Runtime 如何执行？") {
		t.Fatalf("llm prompt = %q", chat.request.Messages[0].Content)
	}
	if messages.content != "Phase 4 已完成" || messages.runID != 4 {
		t.Fatalf("message write = content %q run %d", messages.content, messages.runID)
	}
	if !hasEvent(events.events, runtimeevent.RetrievalStarted, "retrieval_1") || !hasEvent(events.events, runtimeevent.LLMStarted, "llm_1") || !hasEvent(events.events, runtimeevent.MessageCreated, "message_1") {
		t.Fatalf("node events = %+v", events.events)
	}
}

func TestExecutorRunsAgentLoopNode(t *testing.T) {
	chat := &fakeChatClient{toolContent: "Agent Loop 已完成"}
	messages := &fakeMessageWriter{}
	executor := engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{LLM: chat, Providers: fakeProviderLoader{}, Messages: messages}))
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_agent_loop",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "agent_loop_1", Type: "agent_loop", Config: json.RawMessage(`{
				"task_template":"{{sys.query}}",
				"system_prompt":"你是 Agent Loop 节点。",
				"provider_id":7,
				"model":"demo-agent",
				"temperature":0.2,
				"knowledge_top_k":5,
				"knowledge_mode":"keyword",
				"max_iterations":4,
				"max_tool_calls":2,
				"max_execution_time_ms":120000,
				"output_mode":"final_answer",
				"return_intermediate_steps":true
			}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{"content":"{{agent_loop_1.content}}"}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "agent_loop_1"}, {From: "agent_loop_1", To: "message_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "执行 Agent Loop 节点"}}

	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "Agent Loop 已完成" {
		t.Fatalf("content = %v", output["content"])
	}
	if chat.toolRequest.Model != "demo-agent" {
		t.Fatalf("agent model = %q", chat.toolRequest.Model)
	}
	if len(chat.toolRequest.Messages) != 2 || chat.toolRequest.Messages[0].Content != "你是 Agent Loop 节点。" || chat.toolRequest.Messages[1].Content != "执行 Agent Loop 节点" {
		t.Fatalf("agent messages = %+v", chat.toolRequest.Messages)
	}
	if messages.content != "Agent Loop 已完成" {
		t.Fatalf("message write = %q", messages.content)
	}
}

func TestAgentLoopNodeUsesProfileDefaults(t *testing.T) {
	chat := &fakeChatClient{toolContent: "Profile Agent 已完成"}
	profileProviderID := int64(9)
	profiles := fakeProfileLoader{profile: &agent.Profile{
		OwnerID:            1,
		AgentID:            2,
		Role:               "Research Agent",
		Goal:               "Use profile defaults",
		SystemPrompt:       "来自 Profile 的系统提示",
		DefaultProviderID:  &profileProviderID,
		DefaultModel:       "profile-model",
		MaxIterations:      6,
		MaxExecutionTimeMS: 150000,
		MemoryEnabled:      false,
	}}
	executor := engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{LLM: chat, Providers: fakeProviderLoader{}, Profiles: profiles}))
	if err := executor.ValidateNodeConfig("agent_loop", []byte(`{"task_template":"{{sys.query}}"}`)); err != nil {
		t.Fatalf("ValidateNodeConfig() error = %v", err)
	}
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_agent_profile",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "agent_loop_1", Type: "agent_loop", Config: json.RawMessage(`{"task_template":"{{sys.query}}"}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "agent_loop_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "读取 Profile 默认值"}}

	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "Profile Agent 已完成" {
		t.Fatalf("content = %v", output["content"])
	}
	if chat.toolRequest.Model != "profile-model" {
		t.Fatalf("model = %q", chat.toolRequest.Model)
	}
	if len(chat.toolRequest.Messages) != 2 || chat.toolRequest.Messages[0].Content != "来自 Profile 的系统提示" || chat.toolRequest.Messages[1].Content != "读取 Profile 默认值" {
		t.Fatalf("messages = %+v", chat.toolRequest.Messages)
	}
	if len(chat.toolRequest.Tools) != 0 {
		t.Fatalf("unexpected tools: %+v", chat.toolRequest.Tools)
	}
}

func TestAgentLoopNodeExpandsProfileToolPacks(t *testing.T) {
	chat := &fakeChatClient{toolContent: "Profile Pack Agent 已完成"}
	profileProviderID := int64(9)
	profiles := fakeProfileLoader{profile: &agent.Profile{
		OwnerID:              1,
		AgentID:              2,
		Role:                 "Research Agent",
		Goal:                 "Use tool pack defaults",
		SystemPrompt:         "来自 Profile 的系统提示",
		DefaultProviderID:    &profileProviderID,
		DefaultModel:         "profile-model",
		MaxIterations:        6,
		MaxExecutionTimeMS:   150000,
		DefaultToolPackIDs:   json.RawMessage(`[70]`),
		DefaultKnowledgeTopK: 5,
		DefaultKnowledgeMode: "hybrid",
		DefaultMCPServerIDs:  json.RawMessage(`[]`),
		DefaultKnowledgeIDs:  json.RawMessage(`[]`),
		DefaultCallAgentIDs:  json.RawMessage(`[]`),
		DefaultToolIDs:       json.RawMessage(`[]`),
		OutputSchemaJSON:     json.RawMessage(`{}`),
	}}
	registry := &fakeRuntimeToolRegistry{}
	executor := engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{
		LLM:          chat,
		Providers:    fakeProviderLoader{},
		Profiles:     profiles,
		ToolPacks:    fakeToolPackRepo{toolIDs: map[int64][]int64{70: {100, 101}}},
		ToolRegistry: registry,
	}))
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_agent_profile_pack",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "agent_loop_1", Type: "agent_loop", Config: json.RawMessage(`{"task_template":"{{sys.query}}"}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "agent_loop_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "读取 Tool Pack 默认值"}}

	if _, err := executor.Execute(context.Background(), rc, dsl); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(registry.loadedIDs) != 2 || registry.loadedIDs[0] != 100 || registry.loadedIDs[1] != 101 {
		t.Fatalf("expected tool pack ids to be expanded, got %+v", registry.loadedIDs)
	}
	if len(chat.toolRequest.Tools) != 2 || chat.toolRequest.Tools[0].Function.Name != "tool_100" || chat.toolRequest.Tools[1].Function.Name != "tool_101" {
		t.Fatalf("unexpected tools sent to LLM: %+v", chat.toolRequest.Tools)
	}
}

func TestAgentLoopNodeValidatesProfileOutputSchema(t *testing.T) {
	chat := &fakeChatClient{toolContent: `{"answer":"ok"}`}
	profileProviderID := int64(9)
	profiles := fakeProfileLoader{profile: &agent.Profile{
		OwnerID:            1,
		AgentID:            2,
		Role:               "Structured Agent",
		Goal:               "Return JSON",
		SystemPrompt:       "返回 JSON",
		DefaultProviderID:  &profileProviderID,
		DefaultModel:       "profile-model",
		MaxIterations:      6,
		MaxExecutionTimeMS: 150000,
		OutputSchemaJSON:   json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`),
	}}
	executor := engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{LLM: chat, Providers: fakeProviderLoader{}, Profiles: profiles}))
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_agent_profile_schema",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "agent_loop_1", Type: "agent_loop", Config: json.RawMessage(`{"task_template":"{{sys.query}}"}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "agent_loop_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "结构化输出"}}

	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	structured, ok := output["structured_output"].(map[string]any)
	if !ok || structured["answer"] != "ok" {
		t.Fatalf("unexpected structured output: %+v", output["structured_output"])
	}
}

func TestExecutorRunsLegacyAgentLoopNode(t *testing.T) {
	chat := &fakeChatClient{toolContent: "Legacy Agent Loop 已兼容"}
	executor := engine.NewExecutor(runtimenode.DefaultNodes(runtimenode.Deps{LLM: chat, Providers: fakeProviderLoader{}}))
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_agent_loop",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"query":"string"}}`)},
			{ID: "agent_loop_1", Type: "agent_loop", Config: json.RawMessage(`{"provider_id":7,"model":"demo-agent","system_prompt":"legacy","task_template":"{{sys.query}}","max_iterations":2,"max_tool_calls":2}`)},
		},
		Edges: []flow.Edge{{From: "begin_1", To: "agent_loop_1"}},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"query": "旧节点继续运行"}}

	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "Legacy Agent Loop 已兼容" {
		t.Fatalf("content = %v", output["content"])
	}
	if chat.toolRequest.Model != "demo-agent" {
		t.Fatalf("agent model = %q", chat.toolRequest.Model)
	}
	if len(chat.toolRequest.Messages) != 2 || chat.toolRequest.Messages[0].Content != "legacy" || chat.toolRequest.Messages[1].Content != "旧节点继续运行" {
		t.Fatalf("agent messages = %+v", chat.toolRequest.Messages)
	}
}

func TestExecutorRunsSelectedSwitchBranchOnly(t *testing.T) {
	executor := engine.NewExecutor([]engine.Node{runtimenode.BeginNode{}, runtimenode.SwitchNode{}, runtimenode.PromptNode{}, runtimenode.MessageNode{}})
	dsl := &flow.DSL{
		SchemaVersion: flow.SchemaVersionV1,
		FlowID:        "flow_switch",
		Nodes: []flow.Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{"input_schema":{"count":"number"}}`)},
			{ID: "switch_1", Type: "switch", Config: json.RawMessage(`{"conditions":[{"expr":"{{sys.count}} > 0","target":"prompt_yes"},{"expr":"default","target":"prompt_no"}]}`)},
			{ID: "prompt_yes", Type: "prompt", Config: json.RawMessage(`{"template":"yes"}`)},
			{ID: "message_yes", Type: "message", Config: json.RawMessage(`{"content":"{{prompt_yes.prompt}}"}`)},
			{ID: "prompt_no", Type: "prompt", Config: json.RawMessage(`{"template":"no"}`)},
			{ID: "message_no", Type: "message", Config: json.RawMessage(`{"content":"{{prompt_no.prompt}}"}`)},
		},
		Edges: []flow.Edge{
			{From: "begin_1", To: "switch_1"},
			{From: "switch_1", To: "prompt_yes"},
			{From: "prompt_yes", To: "message_yes"},
			{From: "switch_1", To: "prompt_no"},
			{From: "prompt_no", To: "message_no"},
		},
	}
	rc := &engine.RunContext{OwnerID: 1, AgentID: 2, FlowVersionID: 3, RunID: 4, Input: map[string]any{"count": 1}}
	output, err := executor.Execute(context.Background(), rc, dsl)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output["content"] != "yes" {
		t.Fatalf("content = %v", output["content"])
	}
	if rc.ExecutedNodes["prompt_no"] || rc.ExecutedNodes["message_no"] {
		t.Fatalf("unselected branch executed: %+v", rc.ExecutedNodes)
	}
}

type collectingEmitter struct {
	events []runtimeevent.Event
}

func (e *collectingEmitter) Emit(ctx context.Context, event runtimeevent.Event) error {
	e.events = append(e.events, event)
	return nil
}

type fakeRetriever struct {
	request retrieval.RetrievalRequest
}

func (r *fakeRetriever) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	r.request = req
	return &retrieval.RetrievalResponse{LatencyMS: 5, Results: []retrieval.RetrievalResult{{ChunkID: 30, DocumentID: 40, KBID: 10, Score: 0.9, Content: "Runtime 按 DAG 顺序执行节点"}}}, nil
}

type fakeProviderLoader struct{}

func (fakeProviderLoader) LoadChatProviderConfig(ctx context.Context, ownerID, providerID int64, model string) (*runtimenode.LoadedProvider, error) {
	return &runtimenode.LoadedProvider{ProviderID: providerID, Model: model, Config: llm.ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: "http://example.com", APIKey: "test"}}, nil
}

type fakeProfileLoader struct {
	profile *agent.Profile
}

func (l fakeProfileLoader) GetAgentProfile(ctx context.Context, ownerID, agentID int64) (*agent.Profile, error) {
	if l.profile == nil {
		return nil, agenterrors.ErrNotFound
	}
	return l.profile, nil
}

type fakeToolPackRepo struct {
	toolIDs map[int64][]int64
}

func (r fakeToolPackRepo) CreatePack(context.Context, *tool.ToolPack) error { return nil }
func (r fakeToolPackRepo) FindPackByID(context.Context, int64, int64) (*tool.ToolPack, error) {
	return nil, agenterrors.ErrNotFound
}
func (r fakeToolPackRepo) ListPacks(context.Context, int64) ([]tool.ToolPack, error) { return nil, nil }
func (r fakeToolPackRepo) UpdatePack(context.Context, *tool.ToolPack) error          { return nil }
func (r fakeToolPackRepo) DeletePack(context.Context, int64, int64) error            { return nil }
func (r fakeToolPackRepo) AddItem(context.Context, *tool.ToolPackItem) error         { return nil }
func (r fakeToolPackRepo) RemoveItem(context.Context, int64, int64, int64) error     { return nil }
func (r fakeToolPackRepo) ListItems(context.Context, int64, int64) ([]tool.ToolPackItem, error) {
	return nil, nil
}
func (r fakeToolPackRepo) ListToolIDs(_ context.Context, _ int64, packID int64) ([]int64, error) {
	return append([]int64(nil), r.toolIDs[packID]...), nil
}

type fakeRuntimeToolRegistry struct {
	loadedIDs []int64
}

func (r *fakeRuntimeToolRegistry) LoadForAgent(_ context.Context, _ int64, toolIDs []int64) ([]toolruntime.RuntimeTool, error) {
	r.loadedIDs = append([]int64(nil), toolIDs...)
	tools := make([]toolruntime.RuntimeTool, 0, len(toolIDs))
	for _, id := range toolIDs {
		tools = append(tools, fakeRuntimeTool{name: "tool_" + strconv.FormatInt(id, 10)})
	}
	return tools, nil
}

type fakeRuntimeTool struct {
	name string
}

func (t fakeRuntimeTool) Name() string { return t.name }

func (t fakeRuntimeTool) Description() string { return "fake runtime tool" }

func (t fakeRuntimeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t fakeRuntimeTool) Execute(context.Context, toolruntime.ToolRunContext, json.RawMessage) (*toolruntime.ToolResult, error) {
	return &toolruntime.ToolResult{ContentText: "ok"}, nil
}

type fakeChatClient struct {
	request     llm.ChatRequest
	toolRequest llm.ToolChatRequest
	toolContent string
}

func (c *fakeChatClient) Chat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.request = req
	return &llm.ChatResponse{Content: "Phase 4 已完成", Usage: llm.Usage{TotalTokens: 12}}, nil
}

func (c *fakeChatClient) StreamChat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest, onEvent func(llm.StreamEvent) error) error {
	c.request = req
	return onEvent(llm.StreamEvent{Delta: "Phase 4 已完成", Usage: llm.Usage{TotalTokens: 12}, Done: true})
}

func (c *fakeChatClient) ChatWithTools(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.toolRequest = req
	content := c.toolContent
	if content == "" {
		content = "Agent 已完成"
	}
	return &llm.ToolChatResponse{
		Message: llm.ChatMessage{Role: "assistant", Content: content},
		Usage:   llm.Usage{TotalTokens: 8},
	}, nil
}

type fakeMessageWriter struct {
	content string
	runID   int64
}

func (w *fakeMessageWriter) WriteAssistantMessage(ctx context.Context, ownerID int64, conversationID *int64, runID int64, content string, tokenCount int) (int64, error) {
	w.content = content
	w.runID = runID
	return 99, nil
}

func hasEvent(events []runtimeevent.Event, eventType, nodeID string) bool {
	for _, event := range events {
		if event.Type == eventType && event.NodeID == nodeID {
			return true
		}
	}
	return false
}
