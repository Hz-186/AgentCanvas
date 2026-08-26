package pythonbridge

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/parser"
	bridgegen "agentcanvas/internal/infrastructure/pythonbridge/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeBridgeClient struct {
	chunkResponse *bridgegen.ChunkDocumentResponse
	parseResponse *bridgegen.ParseDocumentResponse
	parseRequest  *bridgegen.ParseDocumentRequest
}

type testBridgeServer struct {
	bridgegen.UnimplementedPythonBridgeServer
}

type slowBridgeServer struct {
	bridgegen.UnimplementedPythonBridgeServer
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
	if err != nil || len(capabilities.ChunkMethods) == 0 {
		t.Fatalf("unexpected capabilities: response=%+v error=%v", capabilities, err)
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
}

func TestLivePythonBridgeDocumentParser(t *testing.T) {
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
	capabilities, err := client.GetCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !containsMethod(capabilities.ParserMethods, LangChainPDFParser) {
		t.Skipf("sidecar does not advertise %s", LangChainPDFParser)
	}
	parsed, requiresOCR, _, err := client.ParseDocument(context.Background(), LangChainPDFParser, "scan.pdf", nil)
	if err != nil || !requiresOCR || parsed != nil {
		t.Fatalf("unexpected live parser response: document=%+v requires_ocr=%v error=%v", parsed, requiresOCR, err)
	}
}

func containsMethod(methods []string, target string) bool {
	for _, method := range methods {
		if method == target {
			return true
		}
	}
	return false
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

func (f *fakeBridgeClient) ParseDocument(_ context.Context, request *bridgegen.ParseDocumentRequest, _ ...grpc.CallOption) (*bridgegen.ParseDocumentResponse, error) {
	f.parseRequest = request
	return f.parseResponse, nil
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
	if len(chunks) != 1 || chunks[0].PageNumber == nil || *chunks[0].PageNumber != 3 || chunks[0].Metadata["source"] != "test" {
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

func TestParseDocumentConvertsBlocksAndRequiresOCR(t *testing.T) {
	page := int32(2)
	fake := &fakeBridgeClient{parseResponse: &bridgegen.ParseDocumentResponse{
		Parser: "python:langchain_pdf", ImplementationVersion: "langchain-pymupdf-v1",
		Document: &bridgegen.ParsedDocument{FileType: "pdf", Text: "hello", Blocks: []*bridgegen.DocumentBlock{{
			Id: "p2_b1", Type: "text", Text: "hello", PageNo: &page, BboxJson: `{"x":1,"y":2,"width":3,"height":4}`, MetadataJson: `{"page_no":2}`,
		}}},
	}}
	client := &Client{stub: fake, config: Config{AuthToken: "token", MaxSendBytes: 100, RequestTimeout: time.Second}}
	doc, requiresOCR, _, err := client.ParseDocument(context.Background(), "python:langchain_pdf", "guide.pdf", []byte("pdf"))
	if err != nil || requiresOCR || doc == nil || len(doc.Blocks) != 1 || *doc.Blocks[0].PageNo != 2 || doc.Blocks[0].BBox == nil || doc.Blocks[0].BBox.Width != 3 {
		t.Fatalf("unexpected parsed document: doc=%+v requiresOCR=%v error=%v", doc, requiresOCR, err)
	}
	fake.parseResponse = &bridgegen.ParseDocumentResponse{Parser: "python:langchain_pdf", ImplementationVersion: "v1", RequiresOcr: true}
	doc, requiresOCR, _, err = client.ParseDocument(context.Background(), "python:langchain_pdf", "scan.pdf", []byte("pdf"))
	if err != nil || !requiresOCR || doc != nil {
		t.Fatalf("unexpected OCR response: doc=%+v requiresOCR=%v error=%v", doc, requiresOCR, err)
	}
	fake.parseResponse = &bridgegen.ParseDocumentResponse{
		Parser: "python:langchain_pdf", ImplementationVersion: "v1",
		Document: &bridgegen.ParsedDocument{FileType: "pdf", Text: "missing blocks"},
	}
	if _, _, _, err := client.ParseDocument(context.Background(), "python:langchain_pdf", "broken.pdf", []byte("pdf")); err == nil {
		t.Fatal("ParseDocument accepted text without parsed blocks")
	}
}
