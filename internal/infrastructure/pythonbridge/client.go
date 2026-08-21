package pythonbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/parser"
	bridgegen "agentcanvas/internal/infrastructure/pythonbridge/gen"
	"agentcanvas/internal/runtime/toolruntime"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const ProtocolVersion = "v1"

// IsRetryable reports whether a failed bridge call may be retried by its
// caller. The client intentionally does not retry automatically: ExecuteTool
// may have side effects in future protocol versions, and callers know whether
// their operation is idempotent.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

type Config struct {
	Enabled         bool
	Target          string
	AuthToken       string
	ConnectTimeout  time.Duration
	RequestTimeout  time.Duration
	MaxSendBytes    int
	MaxReceiveBytes int
	MaxConcurrency  int
}

type ToolCapability struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	RiskLevel   string
	SideEffect  string
	Version     string
}

type Capabilities struct {
	ProtocolVersion string
	ServiceVersion  string
	ChunkMethods    []string
	ParserMethods   []string
	Tools           []ToolCapability
	MaxConcurrency  uint32
	MaxInputBytes   uint64
	MaxOutputBytes  uint64
}

type Client struct {
	conn   *grpc.ClientConn
	stub   bridgegen.PythonBridgeClient
	config Config
}

func NewClient(cfg Config) (*Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(cfg.Target) == "" {
		return nil, fmt.Errorf("python bridge target is required when enabled")
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("python bridge auth token is required when enabled")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 2 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxSendBytes <= 0 {
		cfg.MaxSendBytes = 8 * 1024 * 1024
	}
	if cfg.MaxReceiveBytes <= 0 {
		cfg.MaxReceiveBytes = 2 * 1024 * 1024
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 8
	}
	limiter := make(chan struct{}, cfg.MaxConcurrency)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, cfg.Target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			select {
			case limiter <- struct{}{}:
				defer func() { <-limiter }()
			case <-ctx.Done():
				return ctx.Err()
			}
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(cfg.MaxSendBytes),
			grpc.MaxCallRecvMsgSize(cfg.MaxReceiveBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("dial python bridge: %w", err)
	}
	return &Client{conn: conn, stub: bridgegen.NewPythonBridgeClient(conn), config: cfg}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) context(ctx context.Context) (context.Context, context.CancelFunc) {
	return c.contextWithRequestID(ctx, "")
}

func (c *Client) contextWithRequestID(ctx context.Context, requestID string) (context.Context, context.CancelFunc) {
	if c == nil {
		return ctx, func() {}
	}
	if strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	if c.config.RequestTimeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
		ctx = metadata.AppendToOutgoingContext(ctx,
			"x-agentcanvas-bridge-token", c.config.AuthToken,
			"x-agentcanvas-request-id", requestID,
			"x-agentcanvas-trace-id", requestID,
		)
		return ctx, cancel
	}
	ctx = metadata.AppendToOutgoingContext(ctx,
		"x-agentcanvas-bridge-token", c.config.AuthToken,
		"x-agentcanvas-request-id", requestID,
		"x-agentcanvas-trace-id", requestID,
	)
	return ctx, func() {}
}

func (c *Client) Health(ctx context.Context) (*bridgegen.HealthResponse, error) {
	if c == nil || c.stub == nil {
		return nil, fmt.Errorf("python bridge client is not configured")
	}
	callCtx, cancel := c.context(ctx)
	defer cancel()
	return c.stub.Health(callCtx, &bridgegen.HealthRequest{})
}

func (c *Client) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	if c == nil || c.stub == nil {
		return nil, fmt.Errorf("python bridge client is not configured")
	}
	callCtx, cancel := c.context(ctx)
	defer cancel()
	resp, err := c.stub.GetCapabilities(callCtx, &bridgegen.CapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return capabilitiesFromProto(resp)
}

func (c *Client) ChunkDocument(ctx context.Context, method string, doc parser.ParsedDocument, policy chunker.Policy) ([]chunker.Chunk, error) {
	if c == nil || c.stub == nil {
		return nil, fmt.Errorf("python bridge client is not configured")
	}
	document := &bridgegen.ParsedDocument{Text: doc.Text, FileType: doc.FileType, Blocks: make([]*bridgegen.DocumentBlock, 0, len(doc.Blocks))}
	for _, block := range doc.Blocks {
		metadata, err := json.Marshal(block.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal Python block metadata: %w", err)
		}
		item := &bridgegen.DocumentBlock{Id: block.ID, Type: block.Type, Text: block.Text, MetadataJson: string(metadata)}
		if block.PageNo != nil {
			pageNo := int32(*block.PageNo)
			item.PageNo = &pageNo
		}
		if block.BBox != nil {
			bbox, marshalErr := json.Marshal(block.BBox)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal Python block bbox: %w", marshalErr)
			}
			item.BboxJson = string(bbox)
		}
		document.Blocks = append(document.Blocks, item)
	}
	requestID := uuid.NewString()
	callCtx, cancel := c.contextWithRequestID(ctx, requestID)
	defer cancel()
	resp, err := c.stub.ChunkDocument(callCtx, &bridgegen.ChunkDocumentRequest{
		RequestId: requestID, Method: method, Document: document,
		Policy: &bridgegen.ChunkPolicy{ChunkSize: int32(policy.ChunkSize), Overlap: int32(policy.Overlap)},
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("python bridge returned an empty chunk response")
	}
	if strings.TrimSpace(resp.Algorithm) != strings.TrimSpace(method) || strings.TrimSpace(resp.Tokenizer) == "" || strings.TrimSpace(resp.ImplementationVersion) == "" {
		return nil, fmt.Errorf("python bridge returned an invalid chunk algorithm response")
	}
	chunks := make([]chunker.Chunk, 0, len(resp.Chunks))
	for index, item := range resp.Chunks {
		if item == nil || item.Index != int32(index) || strings.TrimSpace(item.Content) == "" {
			return nil, fmt.Errorf("python bridge returned invalid chunk at index %d", index)
		}
		if item.TokenCount <= 0 || item.CharCount != int32(utf8.RuneCountInString(item.Content)) {
			return nil, fmt.Errorf("python bridge returned invalid counts at index %d", index)
		}
		if policy.ChunkSize > 0 && int(item.TokenCount) > policy.ChunkSize {
			return nil, fmt.Errorf("python bridge returned chunk over token budget at index %d", index)
		}
		metadata := map[string]any{}
		if strings.TrimSpace(item.MetadataJson) != "" {
			if err := json.Unmarshal([]byte(item.MetadataJson), &metadata); err != nil {
				return nil, fmt.Errorf("decode Python chunk metadata: %w", err)
			}
			if metadata == nil {
				metadata = map[string]any{}
			}
		}
		var pageNo *int
		if item.PageNo != nil {
			value := int(*item.PageNo)
			pageNo = &value
		}
		chunks = append(chunks, chunker.Chunk{
			Index: index, Content: item.Content, TokenCount: int(item.TokenCount), CharCount: int(item.CharCount),
			SectionTitle: item.SectionTitle, PageNo: pageNo, Metadata: metadata,
		})
	}
	return chunks, nil
}

// ParseDocument asks the sidecar to parse a bounded document payload. The
// returned document remains the AgentCanvas DTO; LangChain types never cross
// the process boundary.
func (c *Client) ParseDocument(ctx context.Context, method, filename string, content []byte) (*parser.ParsedDocument, bool, []string, error) {
	if c == nil || c.stub == nil {
		return nil, false, nil, fmt.Errorf("python bridge client is not configured")
	}
	if strings.TrimSpace(method) == "" || strings.TrimSpace(filename) == "" {
		return nil, false, nil, fmt.Errorf("python bridge parser and filename are required")
	}
	if c.config.MaxSendBytes > 0 && len(content) > c.config.MaxSendBytes {
		return nil, false, nil, fmt.Errorf("document exceeds Python bridge input limit")
	}
	requestID := uuid.NewString()
	callCtx, cancel := c.contextWithRequestID(ctx, requestID)
	defer cancel()
	resp, err := c.stub.ParseDocument(callCtx, &bridgegen.ParseDocumentRequest{
		RequestId: requestID,
		Filename:  filename,
		Parser:    method,
		Content:   content,
	})
	if err != nil {
		return nil, false, nil, err
	}
	if resp == nil || strings.TrimSpace(resp.Parser) != strings.TrimSpace(method) || strings.TrimSpace(resp.ImplementationVersion) == "" {
		return nil, false, nil, fmt.Errorf("python bridge returned an invalid parser response")
	}
	warnings := append([]string(nil), resp.Warnings...)
	if resp.RequiresOcr {
		return nil, true, warnings, nil
	}
	if resp.Document == nil || strings.TrimSpace(resp.Document.FileType) == "" {
		return nil, false, warnings, fmt.Errorf("python bridge returned an empty parsed document")
	}
	doc := &parser.ParsedDocument{Text: resp.Document.Text, FileType: resp.Document.FileType}
	doc.Blocks = make([]parser.DocumentBlock, 0, len(resp.Document.Blocks))
	for index, item := range resp.Document.Blocks {
		if item == nil || strings.TrimSpace(item.Id) == "" || strings.TrimSpace(item.Text) == "" {
			return nil, false, warnings, fmt.Errorf("python bridge returned invalid parsed block at index %d", index)
		}
		metadata := map[string]any{}
		if strings.TrimSpace(item.MetadataJson) != "" {
			if err := json.Unmarshal([]byte(item.MetadataJson), &metadata); err != nil || metadata == nil {
				return nil, false, warnings, fmt.Errorf("decode Python block metadata at index %d", index)
			}
		}
		var pageNo *int
		if item.PageNo != nil {
			value := int(*item.PageNo)
			if value <= 0 {
				return nil, false, warnings, fmt.Errorf("python bridge returned invalid page number at index %d", index)
			}
			pageNo = &value
		}
		var bbox *parser.BBox
		if strings.TrimSpace(item.BboxJson) != "" {
			bbox = &parser.BBox{}
			if err := json.Unmarshal([]byte(item.BboxJson), bbox); err != nil {
				return nil, false, warnings, fmt.Errorf("decode Python block bbox at index %d: %w", index, err)
			}
		}
		doc.Blocks = append(doc.Blocks, parser.DocumentBlock{ID: item.Id, Type: item.Type, Text: item.Text, PageNo: pageNo, BBox: bbox, Metadata: metadata})
	}
	if strings.TrimSpace(doc.Text) == "" || len(doc.Blocks) == 0 {
		return nil, false, warnings, fmt.Errorf("python bridge returned an empty parsed document")
	}
	return doc, false, warnings, nil
}

func (c *Client) ListTools(ctx context.Context) ([]ToolCapability, error) {
	if c == nil || c.stub == nil {
		return nil, fmt.Errorf("python bridge client is not configured")
	}
	callCtx, cancel := c.context(ctx)
	defer cancel()
	resp, err := c.stub.ListTools(callCtx, &bridgegen.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("python bridge returned an empty tools response")
	}
	return toolCapabilitiesFromProto(resp.Tools)
}

func (c *Client) ExecuteTool(ctx context.Context, name string, arguments json.RawMessage, runCtx toolruntime.ToolRunContext) (*bridgegen.ExecuteToolResponse, error) {
	if c == nil || c.stub == nil {
		return nil, fmt.Errorf("python bridge client is not configured")
	}
	requestID := uuid.NewString()
	callCtx, cancel := c.contextWithRequestID(ctx, requestID)
	defer cancel()
	toolContext := &bridgegen.ToolRunContext{
		OwnerId: runCtx.OwnerID, AgentId: runCtx.AgentID, AgentReleaseId: runCtx.AgentReleaseID,
		RunId: runCtx.RunID, DelegationDepth: int32(runCtx.DelegationDepth),
	}
	if runCtx.ConversationID != nil {
		toolContext.ConversationId = *runCtx.ConversationID
	}
	return c.stub.ExecuteTool(callCtx, &bridgegen.ExecuteToolRequest{
		RequestId: requestID, ToolName: name, ArgumentsJson: string(arguments),
		Context: toolContext,
	})
}

func capabilitiesFromProto(resp *bridgegen.CapabilitiesResponse) (*Capabilities, error) {
	if resp == nil {
		return nil, fmt.Errorf("python bridge returned an empty capabilities response")
	}
	if strings.TrimSpace(resp.ProtocolVersion) != ProtocolVersion {
		return nil, fmt.Errorf("unsupported Python bridge protocol version %q", resp.ProtocolVersion)
	}
	tools, err := toolCapabilitiesFromProto(resp.Tools)
	if err != nil {
		return nil, err
	}
	return &Capabilities{
		ProtocolVersion: resp.ProtocolVersion, ServiceVersion: resp.ServiceVersion,
		ChunkMethods: append([]string(nil), resp.ChunkMethods...), ParserMethods: append([]string(nil), resp.ParserMethods...), Tools: tools,
		MaxConcurrency: resp.MaxConcurrency, MaxInputBytes: resp.MaxInputBytes, MaxOutputBytes: resp.MaxOutputBytes,
	}, nil
}

func toolCapabilitiesFromProto(items []*bridgegen.ToolCapability) ([]ToolCapability, error) {
	tools := make([]ToolCapability, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.Name) == "" {
			return nil, fmt.Errorf("python bridge returned an invalid tool capability")
		}
		if !strings.HasPrefix(item.Name, "python_") {
			return nil, fmt.Errorf("python bridge tool %q must use the python_ namespace", item.Name)
		}
		if _, ok := seen[item.Name]; ok {
			return nil, fmt.Errorf("python bridge returned duplicate tool capability %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		parameters := json.RawMessage(item.ParametersJson)
		if !json.Valid(parameters) || strings.TrimSpace(string(parameters)) == "null" {
			return nil, fmt.Errorf("python bridge returned invalid parameters schema for %s", item.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(parameters, &schema); err != nil || schema == nil {
			return nil, fmt.Errorf("python bridge returned non-object parameters schema for %s", item.Name)
		}
		switch strings.TrimSpace(item.RiskLevel) {
		case toolruntime.RiskLow, toolruntime.RiskMedium, toolruntime.RiskHigh:
		default:
			return nil, fmt.Errorf("python bridge returned invalid risk level for %s", item.Name)
		}
		switch strings.TrimSpace(item.SideEffect) {
		case toolruntime.SideEffectNone, toolruntime.SideEffectRead, toolruntime.SideEffectWrite, toolruntime.SideEffectExternalAction:
		default:
			return nil, fmt.Errorf("python bridge returned invalid side effect for %s", item.Name)
		}
		if strings.TrimSpace(item.Version) == "" {
			return nil, fmt.Errorf("python bridge returned an empty version for %s", item.Name)
		}
		tools = append(tools, ToolCapability{Name: item.Name, Description: item.Description, Parameters: parameters, RiskLevel: item.RiskLevel, SideEffect: item.SideEffect, Version: item.Version})
	}
	return tools, nil
}
