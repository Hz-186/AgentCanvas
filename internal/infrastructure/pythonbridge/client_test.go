package pythonbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	tooldomain "agentcanvas/internal/domain/tool"
	"agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/parser"
	bridgegen "agentcanvas/internal/infrastructure/pythonbridge/gen"
	"agentcanvas/internal/runtime/toolruntime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fakeBridgeClient struct {
	chunkResponse  *bridgegen.ChunkDocumentResponse
	executeRequest *bridgegen.ExecuteToolRequest
	executeContext context.Context
}

type testBridgeServer struct {
	bridgegen.UnimplementedPythonBridgeServer
}

type slowBridgeServer struct {
	bridgegen.UnimplementedPythonBridgeServer
}

type fakeInvocationRepository struct {
	items []tooldomain.Invocation
}

func (r *fakeInvocationRepository) Create(_ context.Context, item *tooldomain.Invocation) error {
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeInvocationRepository) ListByRun(context.Context, int64, int64) ([]tooldomain.Invocation, error) {
	return append([]tooldomain.Invocation(nil), r.items...), nil
}

func (testBridgeServer) Health(context.Context, *bridgegen.HealthRequest) (*bridgegen.HealthResponse, error) {
	return &bridgegen.HealthResponse{Status: "ok", ProtocolVersion: ProtocolVersion}, nil
}

func (slowBridgeServer) Health(ctx context.Context, _ *bridgegen.HealthRequest) (*bridgegen.HealthResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLivePythonBridge(t *testing.T) {
	target := os.Getenv("AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET")
	if target == "" {
		t.Skip("AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET is not set")
	}
	client, err := NewClient(Config{
		Enabled: true, Target: target, AuthToken: os.Getenv("AGENTCANVAS_PYTHON_BRIDGE_TOKEN"),
		ConnectTimeout: 3 * time.Second, RequestTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	health, err := client.Health(context.Background())
	if err != nil || health.GetStatus() != "ok" || health.GetProtocolVersion() != ProtocolVersion {
		t.Fatalf("unexpected health response: response=%+v error=%v", health, err)
	}
	capabilities, err := client.GetCapabilities(context.Background())
	if err != nil || len(capabilities.ChunkMethods) == 0 || len(capabilities.Tools) == 0 {
		t.Fatalf("unexpected capabilities: response=%+v error=%v", capabilities, err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != len(capabilities.Tools) {
		t.Fatalf("unexpected ListTools response: tools=%+v error=%v", tools, err)
	}
	chunks, err := client.ChunkDocument(context.Background(), "python:recursive", parser.ParsedDocument{
		Text: "# 标题\n\n第一段。第二段。",
		Blocks: []parser.DocumentBlock{
			{ID: "h1", Type: "heading", Text: "# 标题"},
			{ID: "b1", Type: "paragraph", Text: "第一段。第二段。", Metadata: map[string]any{"source": "integration"}},
		},
	}, chunker.Policy{ChunkSize: 8, Overlap: 1})
	if err != nil || len(chunks) == 0 || chunks[0].SectionTitle != "标题" {
		t.Fatalf("unexpected chunks: chunks=%+v error=%v", chunks, err)
	}
	response, err := client.ExecuteTool(context.Background(), "python_text_stats", json.RawMessage(`{"text":"hello"}`), toolruntime.ToolRunContext{OwnerID: 1, AgentID: 2, RunID: 3})
	if err != nil || response.GetIsError() || !json.Valid([]byte(response.GetContentJson())) {
		t.Fatalf("unexpected tool response: response=%+v error=%v", response, err)
	}
}

func TestClientReportsUnavailableAndReconnectsAfterServerRestart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	server := grpc.NewServer()
	bridgegen.RegisterPythonBridgeServer(server, testBridgeServer{})
	go func() { _ = server.Serve(listener) }()

	client, err := NewClient(Config{Enabled: true, Target: address, AuthToken: "token", ConnectTimeout: time.Second, RequestTimeout: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatalf("initial Health() error = %v", err)
	}
	server.Stop()
	_ = listener.Close()
	if _, err := client.Health(context.Background()); status.Code(err) != codes.Unavailable {
		t.Fatalf("Health() after stop code = %s, want UNAVAILABLE: %v", status.Code(err), err)
	}

	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	restarted := grpc.NewServer()
	bridgegen.RegisterPythonBridgeServer(restarted, testBridgeServer{})
	go func() { _ = restarted.Serve(listener) }()
	t.Cleanup(func() {
		restarted.Stop()
		_ = listener.Close()
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = client.Health(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client did not reconnect: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestClientHonorsDeadlineAndCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	bridgegen.RegisterPythonBridgeServer(server, slowBridgeServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	client, err := NewClient(Config{Enabled: true, Target: listener.Addr().String(), AuthToken: "token", ConnectTimeout: time.Second, RequestTimeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Health(context.Background()); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("Health() deadline code = %s, want DEADLINE_EXCEEDED: %v", status.Code(err), err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Health(cancelled); status.Code(err) != codes.Canceled && !errors.Is(err, context.Canceled) {
		t.Fatalf("Health() cancellation code = %s, want CANCELED: %v", status.Code(err), err)
	}
}

func TestIsRetryableUsesSafeBridgeErrorCodes(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		if !IsRetryable(status.Error(code, "temporary bridge failure")) {
			t.Fatalf("status %s must be retryable", code)
		}
	}
	for _, code := range []codes.Code{codes.InvalidArgument, codes.Unauthenticated, codes.ResourceExhausted, codes.Canceled} {
		if IsRetryable(status.Error(code, "non-retryable bridge failure")) {
			t.Fatalf("status %s must not be retryable", code)
		}
	}
}

func (f *fakeBridgeClient) Health(context.Context, *bridgegen.HealthRequest, ...grpc.CallOption) (*bridgegen.HealthResponse, error) {
	return &bridgegen.HealthResponse{Status: "ok"}, nil
}

func (f *fakeBridgeClient) GetCapabilities(context.Context, *bridgegen.CapabilitiesRequest, ...grpc.CallOption) (*bridgegen.CapabilitiesResponse, error) {
	return nil, errors.New("not used")
}

func (f *fakeBridgeClient) ChunkDocument(_ context.Context, _ *bridgegen.ChunkDocumentRequest, _ ...grpc.CallOption) (*bridgegen.ChunkDocumentResponse, error) {
	return f.chunkResponse, nil
}

func (f *fakeBridgeClient) ListTools(context.Context, *bridgegen.ListToolsRequest, ...grpc.CallOption) (*bridgegen.ListToolsResponse, error) {
	return nil, errors.New("not used")
}

func (f *fakeBridgeClient) ExecuteTool(ctx context.Context, request *bridgegen.ExecuteToolRequest, _ ...grpc.CallOption) (*bridgegen.ExecuteToolResponse, error) {
	f.executeRequest = request
	f.executeContext = ctx
	return &bridgegen.ExecuteToolResponse{ContentJson: `{"ok":true}`, ContentText: "ok"}, nil
}

func TestCapabilitiesRejectsUnknownProtocol(t *testing.T) {
	if _, err := capabilitiesFromProto(&bridgegen.CapabilitiesResponse{ProtocolVersion: "v2"}); err == nil {
		t.Fatal("capabilitiesFromProto must reject an unknown protocol version")
	}
}

func TestChunkDocumentConvertsResponseAndContext(t *testing.T) {
	page := int32(3)
	fake := &fakeBridgeClient{chunkResponse: &bridgegen.ChunkDocumentResponse{Chunks: []*bridgegen.Chunk{{
		Index: 0, Content: "hello", TokenCount: 2, CharCount: 5, PageNo: &page, MetadataJson: `{"source":"test"}`,
	}}, Algorithm: "python:fixed_token", Tokenizer: "estimated", ImplementationVersion: "0.1.0"}}
	client := &Client{stub: fake, config: Config{AuthToken: "token", RequestTimeout: time.Second}}
	chunks, err := client.ChunkDocument(context.Background(), "python:fixed_token", parser.ParsedDocument{Text: "hello"}, chunker.Policy{ChunkSize: 4})
	if err != nil {
		t.Fatalf("ChunkDocument() error = %v", err)
	}
	if len(chunks) != 1 || chunks[0].PageNo == nil || *chunks[0].PageNo != 3 || chunks[0].Metadata["source"] != "test" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestChunkDocumentRejectsInvalidAlgorithmResponse(t *testing.T) {
	fake := &fakeBridgeClient{chunkResponse: &bridgegen.ChunkDocumentResponse{Algorithm: "python:recursive", Tokenizer: "estimated", ImplementationVersion: "1"}}
	client := &Client{stub: fake, config: Config{AuthToken: "token", RequestTimeout: time.Second}}
	if _, err := client.ChunkDocument(context.Background(), "python:fixed_token", parser.ParsedDocument{Text: "hello"}, chunker.Policy{ChunkSize: 4}); err == nil {
		t.Fatal("ChunkDocument accepted a response for a different algorithm")
	}
}

func TestExecuteToolPropagatesConversationAndAuthMetadata(t *testing.T) {
	fake := &fakeBridgeClient{}
	client := &Client{stub: fake, config: Config{AuthToken: "token", RequestTimeout: time.Second}}
	conversationID := int64(42)
	_, err := client.ExecuteTool(context.Background(), "python_text_stats", []byte(`{"text":"hello"}`), toolruntime.ToolRunContext{OwnerID: 1, AgentID: 2, RunID: 3, ConversationID: &conversationID})
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if fake.executeRequest.Context.GetConversationId() != conversationID {
		t.Fatalf("conversation id = %d, want %d", fake.executeRequest.Context.GetConversationId(), conversationID)
	}
	values, ok := metadata.FromOutgoingContext(fake.executeContext)
	if !ok || len(values.Get("x-agentcanvas-bridge-token")) != 1 || values.Get("x-agentcanvas-bridge-token")[0] != "token" {
		t.Fatalf("bridge auth metadata missing: %v", values)
	}
	if values.Get("x-agentcanvas-request-id")[0] != fake.executeRequest.RequestId || values.Get("x-agentcanvas-trace-id")[0] == "" {
		t.Fatalf("bridge correlation metadata missing: values=%v request=%s", values, fake.executeRequest.RequestId)
	}
}

func TestRuntimeToolPersistsInvocationAudit(t *testing.T) {
	repository := &fakeInvocationRepository{}
	runtimeTool := RuntimeTool{
		Client:      &Client{stub: &fakeBridgeClient{}, config: Config{AuthToken: "token", RequestTimeout: time.Second}},
		Capability:  ToolCapability{Name: "python_text_stats", RiskLevel: "low", SideEffect: "none"},
		Invocations: repository,
	}
	result, err := runtimeTool.Execute(context.Background(), toolruntime.ToolRunContext{OwnerID: 1, AgentID: 2, RunID: 3}, json.RawMessage(`{"text":"hello"}`))
	if err != nil || result.ContentText != "ok" {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(repository.items) != 1 || repository.items[0].ToolType != "python_bridge" || repository.items[0].Status != tooldomain.InvocationStatusSucceeded {
		t.Fatalf("unexpected invocation audit: %+v", repository.items)
	}
}

func TestToolCapabilitiesFailClosedOnUnsafeMetadata(t *testing.T) {
	base := &bridgegen.ToolCapability{
		Name: "python_test", ParametersJson: `{"type":"object"}`, RiskLevel: "low", SideEffect: "none", Version: "1",
	}
	for _, mutate := range []func(*bridgegen.ToolCapability){
		func(item *bridgegen.ToolCapability) { item.Name = "workspace_exec" },
		func(item *bridgegen.ToolCapability) { item.RiskLevel = "unknown" },
		func(item *bridgegen.ToolCapability) { item.SideEffect = "unknown" },
		func(item *bridgegen.ToolCapability) { item.Version = "" },
		func(item *bridgegen.ToolCapability) { item.ParametersJson = "[]" },
	} {
		item := proto.Clone(base).(*bridgegen.ToolCapability)
		mutate(item)
		if _, err := toolCapabilitiesFromProto([]*bridgegen.ToolCapability{item}); err == nil {
			t.Fatalf("toolCapabilitiesFromProto accepted unsafe capability: name=%s risk=%s side_effect=%s", item.Name, item.RiskLevel, item.SideEffect)
		}
	}
}
