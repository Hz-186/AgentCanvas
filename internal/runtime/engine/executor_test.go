package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	runtimenode "agentcanvas/internal/runtime/node"
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

type fakeChatClient struct {
	request llm.ChatRequest
}

func (c *fakeChatClient) Chat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.request = req
	return &llm.ChatResponse{Content: "Phase 4 已完成", Usage: llm.Usage{TotalTokens: 12}}, nil
}

func (c *fakeChatClient) StreamChat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest, onEvent func(llm.StreamEvent) error) error {
	c.request = req
	return onEvent(llm.StreamEvent{Delta: "Phase 4 已完成", Usage: llm.Usage{TotalTokens: 12}, Done: true})
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
