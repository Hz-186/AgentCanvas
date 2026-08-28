package toolruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentcanvas/internal/domain/tool"
)

const (
	MCPTransportStreamableHTTP = "streamable_http"
	MCPTransportStdio          = "stdio"
	mcpProtocolVersion         = "2025-11-25"
)

//	MCPClient In SSE mode, the MCP client might be like {
//		"name": "My Weather Server",
//		"sse_url": "https://weather-mcp.example.com/sse",
//		"transport": "streamable_http"
//	}
//
//	In stdio mode, it might be like {
//	 "name": "Local Python Tools",
//	 "transport": "stdio",
//	 "command": "python3",
//	 "args": ["/home/user/mcp_servers/my_server.py"],
//	 "env": {"API_KEY": "sk-xxx", "DEBUG": "1"}
//	}
type MCPClient struct {
	Name       string
	SSEURL     string            // MCP endpoint in Streamable HTTP mode; kept for API compatibility.
	Transport  string            // streamable_http or stdio
	Command    string            // stdio mode: "python"、"node"
	Args       []string          // stdio mode: ["server.py"]
	Env        map[string]string // stdio mode: env
	HTTPClient *http.Client
	mu         sync.RWMutex // keep tools/cachedAt/lastError SAVE
	tools      []MCPToolDef
	cachedAt   time.Time
	lastError  error
	stdioMu    sync.Mutex
	stdio      *mcpStdioSession
	httpMu     sync.Mutex
	sessionID  string
	protocol   string
	httpReady  bool
	nextID     atomic.Int64
	lastUsed   atomic.Int64
}

type MCPToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

func (d *MCPToolDef) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.Name, d.Description = raw.Name, raw.Description
	d.Parameters = raw.InputSchema
	if len(d.Parameters) == 0 {
		d.Parameters = raw.Parameters
	}
	return nil
}

type MCPToolRuntime struct {
	def    MCPToolDef
	client *MCPClient
}

type mcpHTTPStatusError struct {
	Status int
	Body   string
}

func (e *mcpHTTPStatusError) Error() string {
	return fmt.Sprintf("mcp server error: status=%d body=%s", e.Status, e.Body)
}

func NewMCPToolRuntime(def MCPToolDef, client *MCPClient) RuntimeTool {
	return &MCPToolRuntime{def: def, client: client}
}

func NewMCPClient(name, sseURL string) *MCPClient {
	client := &MCPClient{
		Name:      name,
		SSEURL:    sseURL,
		Transport: MCPTransportStreamableHTTP,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
	client.touch()
	return client
}

func NewMCPStdioClient(name, command string, args []string, env map[string]string) *MCPClient {
	client := &MCPClient{
		Name:      name,
		Transport: MCPTransportStdio,
		Command:   command,
		Args:      append([]string(nil), args...),
		Env:       cloneStringMap(env),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
	client.touch()
	return client
}

func NewMCPClientFromServer(server *tool.MCPServer) *MCPClient {
	mcpPoolJanitorOnce.Do(startMCPPoolJanitor)
	keyBytes, _ := json.Marshal([]any{server.OwnerID, server.ID, server.Transport, server.EndpointURL, server.Command, server.ArgsJSON, server.EnvJSON})
	key := string(keyBytes)
	if cached, ok := defaultMCPClients.Load(key); ok {
		client := cached.(*MCPClient)
		client.touch()
		return client
	}
	var client *MCPClient
	if server.Transport == tool.MCPTransportStdio {
		client = NewMCPStdioClient(server.Name, server.Command, server.ArgsSlice(), server.EnvMap())
	} else {
		client = NewMCPClient(server.Name, server.EndpointURL)
	}
	actual, _ := defaultMCPClients.LoadOrStore(key, client)
	pooled := actual.(*MCPClient)
	pooled.touch()
	return pooled
}

var defaultMCPClients sync.Map
var mcpPoolJanitorOnce sync.Once

func startMCPPoolJanitor() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-5 * time.Minute).UnixNano()
			defaultMCPClients.Range(func(key, value any) bool {
				client, ok := value.(*MCPClient)
				if ok && client.lastUsed.Load() < cutoff {
					defaultMCPClients.Delete(key)
					if client.lastUsed.Load() < cutoff {
						client.closeIdleSessions()
					} else {
						defaultMCPClients.Store(key, client)
					}
				}
				return true
			})
		}
	}()
}

func (c *MCPClient) touch() { c.lastUsed.Store(time.Now().UnixNano()) }

func (c *MCPClient) closeIdleSessions() {
	c.stdioMu.Lock()
	if c.stdio != nil {
		c.stdio.close(io.EOF)
		c.stdio = nil
	}
	c.stdioMu.Unlock()
	c.httpMu.Lock()
	if c.httpReady && c.sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimSpace(c.SSEURL), nil)
		req.Header.Set("MCP-Session-Id", c.sessionID)
		req.Header.Set("MCP-Protocol-Version", c.protocol)
		if resp, err := c.HTTPClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
		cancel()
	}
	c.httpReady, c.sessionID, c.protocol = false, "", ""
	c.httpMu.Unlock()
}

// Discover Double-Check Locking
func (c *MCPClient) Discover(ctx context.Context) ([]MCPToolDef, error) {
	c.touch()
	c.mu.RLock()
	if len(c.tools) > 0 && time.Since(c.cachedAt) < 5*time.Minute {
		result := make([]MCPToolDef, len(c.tools))
		copy(result, c.tools)
		c.mu.RUnlock()
		return result, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.tools) > 0 && time.Since(c.cachedAt) < 5*time.Minute {
		result := make([]MCPToolDef, len(c.tools))
		copy(result, c.tools)
		return result, nil
	}

	tools, err := c.fetchTools(ctx)
	if err != nil {
		c.lastError = err
		return nil, err
	}
	c.tools = tools
	c.cachedAt = time.Now()
	c.lastError = nil
	result := make([]MCPToolDef, len(tools))
	copy(result, tools)
	return result, nil
}

func (c *MCPClient) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = nil
	c.cachedAt = time.Time{}
}

func (c *MCPClient) CallTool(ctx context.Context, toolName string, args json.RawMessage) (*ToolResult, error) {
	c.touch()
	if c.Transport == MCPTransportStdio {
		return c.callStdioTool(ctx, toolName, args)
	}
	if err := c.ensureHTTPInitialized(ctx); err != nil {
		return nil, err
	}
	var arguments any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return nil, fmt.Errorf("invalid mcp tool arguments: %w", err)
		}
	}
	result, err := c.httpRPC(ctx, "tools/call", map[string]any{"name": toolName, "arguments": arguments})
	if err != nil {
		if isMCPSessionExpired(err) {
			c.resetHTTPSession()
		}
		return nil, err
	}
	return &ToolResult{ContentJSON: result, ContentText: string(result)}, nil
}

// fetchTools is part of Discover
func (c *MCPClient) fetchTools(ctx context.Context) ([]MCPToolDef, error) {
	if c.Transport == MCPTransportStdio {
		return c.fetchStdioTools(ctx)
	}
	if err := c.ensureHTTPInitialized(ctx); err != nil {
		return nil, err
	}
	result, err := c.httpRPC(ctx, "tools/list", map[string]any{})
	if isMCPSessionExpired(err) {
		c.resetHTTPSession()
		if initErr := c.ensureHTTPInitialized(ctx); initErr != nil {
			return nil, initErr
		}
		result, err = c.httpRPC(ctx, "tools/list", map[string]any{})
	}
	if err != nil {
		return nil, err
	}
	var parsed mcpListToolsResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("mcp discover failed: %w", err)
	}
	tools := parsed.Tools
	if len(tools) == 0 {
		return nil, fmt.Errorf("no tools discovered from mcp server %s", c.SSEURL)
	}
	return tools, nil
}

func (c *MCPClient) ensureHTTPInitialized(ctx context.Context) error {
	c.httpMu.Lock()
	defer c.httpMu.Unlock()
	if c.httpReady {
		return nil
	}
	request := mcpJSONRPCRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: "initialize", Params: map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "AgentCanvas", "version": "dev"},
	}}
	response, headers, err := c.postHTTPMessage(ctx, request, false)
	if err != nil {
		return err
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(response.Result, &initialized); err != nil {
		return fmt.Errorf("mcp initialize failed: %w", err)
	}
	if strings.TrimSpace(initialized.ProtocolVersion) == "" {
		return fmt.Errorf("mcp initialize failed: protocol version is missing")
	}
	c.protocol = initialized.ProtocolVersion
	c.sessionID = headers.Get("MCP-Session-Id")
	_, _, err = c.postHTTPMessage(ctx, mcpJSONRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}, true)
	if err != nil {
		c.protocol, c.sessionID = "", ""
		return err
	}
	c.httpReady = true
	return nil
}

func (c *MCPClient) httpRPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.httpMu.Lock()
	defer c.httpMu.Unlock()
	request := mcpJSONRPCRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params}
	response, _, err := c.postHTTPMessage(ctx, request, false)
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func (c *MCPClient) postHTTPMessage(ctx context.Context, message mcpJSONRPCRequest, notification bool) (mcpJSONRPCResponse, http.Header, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return mcpJSONRPCResponse{}, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(c.SSEURL), strings.NewReader(string(body)))
	if err != nil {
		return mcpJSONRPCResponse{}, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", c.protocol)
	}
	if c.sessionID != "" {
		req.Header.Set("MCP-Session-Id", c.sessionID)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return mcpJSONRPCResponse{}, nil, err
	}
	defer resp.Body.Close()
	if notification && resp.StatusCode == http.StatusAccepted {
		return mcpJSONRPCResponse{}, resp.Header, nil
	}
	if resp.StatusCode >= 400 {
		responseBody, _ := io.ReadAll(resp.Body)
		return mcpJSONRPCResponse{}, resp.Header, &mcpHTTPStatusError{Status: resp.StatusCode, Body: strings.TrimSpace(string(responseBody))}
	}
	response, err := decodeHTTPRPCResponse(resp.Body, resp.Header.Get("Content-Type"), message.ID)
	return response, resp.Header, err
}

func isMCPSessionExpired(err error) bool {
	var statusErr *mcpHTTPStatusError
	return errors.As(err, &statusErr) && statusErr.Status == http.StatusNotFound
}

func (c *MCPClient) resetHTTPSession() {
	c.httpMu.Lock()
	c.httpReady, c.sessionID, c.protocol = false, "", ""
	c.httpMu.Unlock()
}

func decodeHTTPRPCResponse(reader io.Reader, contentType string, expectedID int64) (mcpJSONRPCResponse, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var response mcpJSONRPCResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &response); err == nil && response.ID == expectedID {
				if response.Error != nil {
					return response, fmt.Errorf("mcp error %d: %s", response.Error.Code, response.Error.Message)
				}
				return response, nil
			}
		}
		if err := scanner.Err(); err != nil {
			return mcpJSONRPCResponse{}, err
		}
		return mcpJSONRPCResponse{}, fmt.Errorf("mcp SSE response missing request id %d", expectedID)
	}
	var response mcpJSONRPCResponse
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return response, err
	}
	if response.Error != nil {
		return response, fmt.Errorf("mcp error %d: %s", response.Error.Code, response.Error.Message)
	}
	if response.ID != expectedID {
		return response, fmt.Errorf("mcp response id mismatch: got %d want %d", response.ID, expectedID)
	}
	return response, nil
}

func (t *MCPToolRuntime) Name() string                { return t.def.Name }
func (t *MCPToolRuntime) Description() string         { return t.def.Description }
func (t *MCPToolRuntime) Parameters() json.RawMessage { return t.def.Parameters }
func (t *MCPToolRuntime) Metadata() ToolMetadata {
	metadata := ToolMetadata{
		RiskLevel:  RiskMedium,
		SideEffect: SideEffectExternalAction,
	}
	if t.client != nil && t.client.SSEURL != "" {
		if endpoint, err := url.Parse(strings.TrimSpace(t.client.SSEURL)); err == nil && endpoint.Hostname() != "" {
			metadata.AllowedHosts = []string{endpoint.Hostname()}
		}
	}
	return metadata
}
func (t *MCPToolRuntime) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	return t.client.CallTool(ctx, t.def.Name, input)
}

type mcpJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpListToolsResult struct {
	Tools []MCPToolDef `json:"tools"`
}

func (c *MCPClient) fetchStdioTools(ctx context.Context) ([]MCPToolDef, error) {
	session, err := c.ensureStdioSession(ctx)
	if err != nil {
		return nil, err
	}
	result, err := session.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		c.invalidateStdioSession(session)
		return nil, err
	}
	var parsed mcpListToolsResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("mcp stdio discover failed: %w", err)
	}
	if len(parsed.Tools) == 0 {
		return nil, fmt.Errorf("no tools discovered from mcp stdio server %s", c.Command)
	}
	return parsed.Tools, nil
}

func (c *MCPClient) callStdioTool(ctx context.Context, toolName string, args json.RawMessage) (*ToolResult, error) {
	var arguments any
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return nil, fmt.Errorf("invalid mcp tool arguments: %w", err)
		}
	} else {
		arguments = map[string]any{}
	}
	session, err := c.ensureStdioSession(ctx)
	if err != nil {
		return nil, err
	}
	result, err := session.request(ctx, "tools/call", map[string]any{"name": toolName, "arguments": arguments})
	if err != nil {
		// Never replay a tool call whose outcome is unknown. Reconnect only for a later call.
		c.invalidateStdioSession(session)
		return nil, err
	}
	return &ToolResult{ContentJSON: result, ContentText: string(result)}, nil
}

type mcpStdioSession struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[int64]chan mcpJSONRPCResponse
	nextID    atomic.Int64
	done      chan struct{}
	closeOnce sync.Once
	err       error
}

func (c *MCPClient) ensureStdioSession(ctx context.Context) (*mcpStdioSession, error) {
	c.stdioMu.Lock()
	defer c.stdioMu.Unlock()
	if c.stdio != nil && !c.stdio.closed() {
		return c.stdio, nil
	}
	session, err := startMCPStdioSession(c.Command, c.Args, c.Env)
	if err != nil {
		return nil, err
	}
	if _, err := session.request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "AgentCanvas", "version": "dev"},
	}); err != nil {
		session.close(err)
		return nil, err
	}
	if err := session.notify("notifications/initialized", nil); err != nil {
		session.close(err)
		return nil, err
	}
	c.stdio = session
	return session, nil
}

func (c *MCPClient) invalidateStdioSession(session *mcpStdioSession) {
	c.stdioMu.Lock()
	if c.stdio == session {
		c.stdio = nil
	}
	c.stdioMu.Unlock()
}

func startMCPStdioSession(command string, args []string, env map[string]string) (*mcpStdioSession, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("mcp stdio command is required")
	}
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), envPairs(env)...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	session := &mcpStdioSession{cmd: cmd, stdin: stdin, pending: map[int64]chan mcpJSONRPCResponse{}, done: make(chan struct{})}
	go io.Copy(io.Discard, stderr)
	go session.readLoop(stdout)
	return session, nil
}

func (s *mcpStdioSession) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var response mcpJSONRPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.ID == 0 {
			continue
		}
		s.pendingMu.Lock()
		ch := s.pending[response.ID]
		delete(s.pending, response.ID)
		s.pendingMu.Unlock()
		if ch != nil {
			ch <- response
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	s.close(err)
}

func (s *mcpStdioSession) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan mcpJSONRPCResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()
	if err := s.write(mcpJSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return nil, fmt.Errorf("mcp stdio error %d: %s", response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, fmt.Errorf("mcp stdio session closed: %w", s.err)
	}
}

func (s *mcpStdioSession) notify(method string, params any) error {
	return s.write(mcpJSONRPCRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *mcpStdioSession) write(request mcpJSONRPCRequest) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return fmt.Errorf("mcp stdio session closed: %w", s.err)
	default:
	}
	return json.NewEncoder(s.stdin).Encode(request)
}

func (s *mcpStdioSession) close(err error) {
	s.closeOnce.Do(func() {
		s.err = err
		close(s.done)
		_ = s.stdin.Close()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	})
}

func (s *mcpStdioSession) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func envPairs(input map[string]string) []string {
	pairs := make([]string, 0, len(input))
	for k, v := range input {
		pairs = append(pairs, k+"="+v)
	}
	return pairs
}
