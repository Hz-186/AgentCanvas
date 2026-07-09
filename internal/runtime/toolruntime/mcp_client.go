package toolruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/domain/tool"
)

const (
	MCPTransportSSE   = "sse"
	MCPTransportStdio = "stdio"
)

type MCPClient struct {
	Name       string
	SSEURL     string
	Transport  string
	Command    string
	Args       []string
	Env        map[string]string
	HTTPClient *http.Client
	mu         sync.RWMutex
	tools      []MCPToolDef
	cachedAt   time.Time
	lastError  error
}

type MCPToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type MCPToolRuntime struct {
	def    MCPToolDef
	client *MCPClient
}

func NewMCPToolRuntime(def MCPToolDef, client *MCPClient) RuntimeTool {
	return &MCPToolRuntime{def: def, client: client}
}

func NewMCPClient(name, sseURL string) *MCPClient {
	return &MCPClient{
		Name:      name,
		SSEURL:    sseURL,
		Transport: MCPTransportSSE,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func NewMCPStdioClient(name, command string, args []string, env map[string]string) *MCPClient {
	return &MCPClient{
		Name:      name,
		Transport: MCPTransportStdio,
		Command:   command,
		Args:      append([]string(nil), args...),
		Env:       cloneStringMap(env),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func NewMCPClientFromServer(server *tool.MCPServer) *MCPClient {
	if server.Transport == tool.MCPTransportStdio {
		return NewMCPStdioClient(server.Name, server.Command, server.ArgsSlice(), server.EnvMap())
	}
	return NewMCPClient(server.Name, server.EndpointURL)
}

func (c *MCPClient) Discover(ctx context.Context) ([]MCPToolDef, error) {
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

func (c *MCPClient) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tools, err := c.fetchTools(ctx)
	if err != nil {
		c.lastError = err
		return err
	}
	c.tools = tools
	c.cachedAt = time.Now()
	c.lastError = nil
	return nil
}

func (c *MCPClient) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = nil
	c.cachedAt = time.Time{}
}

func (c *MCPClient) CallTool(ctx context.Context, toolName string, args json.RawMessage) (*ToolResult, error) {
	if c.Transport == MCPTransportStdio {
		return c.callStdioTool(ctx, toolName, args)
	}
	reqBody, err := json.Marshal(map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.SSEURL, "/")+"/tools/call", strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp server error: status=%d body=%s", resp.StatusCode, string(body))
	}
	return &ToolResult{ContentJSON: json.RawMessage(body)}, nil
}

func (c *MCPClient) fetchTools(ctx context.Context) ([]MCPToolDef, error) {
	if c.Transport == MCPTransportStdio {
		return c.fetchStdioTools(ctx)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.SSEURL, "/")+"/tools", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mcp discover failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	reader := bufio.NewReader(resp.Body)
	var tools []MCPToolDef
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if toolList, ok := event["tools"]; ok {
			if err := json.Unmarshal(toolList, &tools); err == nil {
				break
			}
		}
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("no tools discovered from mcp server %s", c.SSEURL)
	}
	return tools, nil
}

func (t *MCPToolRuntime) Name() string                { return t.def.Name }
func (t *MCPToolRuntime) Description() string         { return t.def.Description }
func (t *MCPToolRuntime) Parameters() json.RawMessage { return t.def.Parameters }
func (t *MCPToolRuntime) Metadata() ToolMetadata {
	metadata := ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectExternalAction}
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
	responses, err := c.runStdioSession(ctx, []mcpJSONRPCRequest{
		{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "AgentCanvas", "version": "dev"},
		}},
		{JSONRPC: "2.0", Method: "notifications/initialized"},
		{JSONRPC: "2.0", ID: 2, Method: "tools/list"},
	})
	if err != nil {
		return nil, err
	}
	result, ok := responses[2]
	if !ok {
		return nil, fmt.Errorf("mcp stdio discover failed: missing tools/list response")
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
	responses, err := c.runStdioSession(ctx, []mcpJSONRPCRequest{
		{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "AgentCanvas", "version": "dev"},
		}},
		{JSONRPC: "2.0", Method: "notifications/initialized"},
		{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: map[string]any{
			"name":      toolName,
			"arguments": arguments,
		}},
	})
	if err != nil {
		return nil, err
	}
	result, ok := responses[2]
	if !ok {
		return nil, fmt.Errorf("mcp stdio tool call failed: missing tools/call response")
	}
	return &ToolResult{ContentJSON: result, ContentText: string(result)}, nil
}

func (c *MCPClient) runStdioSession(ctx context.Context, requests []mcpJSONRPCRequest) (map[int64]json.RawMessage, error) {
	if strings.TrimSpace(c.Command) == "" {
		return nil, fmt.Errorf("mcp stdio command is required")
	}
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	if len(c.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envPairs(c.Env)...)
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

	responsesCh := make(chan map[int64]json.RawMessage, 1)
	errCh := make(chan error, 2)
	go func() {
		responses := map[int64]json.RawMessage{}
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var resp mcpJSONRPCResponse
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				continue
			}
			if resp.ID == 0 {
				continue
			}
			if resp.Error != nil {
				errCh <- fmt.Errorf("mcp stdio error %d: %s", resp.Error.Code, resp.Error.Message)
				return
			}
			responses[resp.ID] = resp.Result
			if len(responses) >= countRequestIDs(requests) {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}
		responsesCh <- responses
	}()

	encoder := json.NewEncoder(stdin)
	for _, req := range requests {
		if err := encoder.Encode(req); err != nil {
			_ = stdin.Close()
			_ = cmd.Wait()
			return nil, err
		}
	}
	_ = stdin.Close()

	var responses map[int64]json.RawMessage
	select {
	case responses = <-responsesCh:
	case err := <-errCh:
		_ = cmd.Wait()
		return nil, err
	case <-ctx.Done():
		_ = cmd.Wait()
		return nil, ctx.Err()
	}
	stderrBytes, _ := io.ReadAll(stderr)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("mcp stdio command failed: %w stderr=%s", err, strings.TrimSpace(string(stderrBytes)))
	}
	return responses, nil
}

func countRequestIDs(requests []mcpJSONRPCRequest) int {
	count := 0
	for _, req := range requests {
		if req.ID != 0 {
			count++
		}
	}
	return count
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
