package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type HTTPToolNode struct {
	Tools       tool.DefinitionRepository
	Invocations tool.InvocationRepository
}

type httpToolConfig struct {
	ToolID int64          `json:"tool_id"`
	Input  map[string]any `json:"input"`
}

type httpToolDefinitionConfig struct {
	URL              string            `json:"url"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	TimeoutMS        int               `json:"timeout_ms"`
	MaxResponseBytes int64             `json:"max_response_bytes"`
}

func (HTTPToolNode) Type() string { return "http_tool" }

func (HTTPToolNode) Validate(config json.RawMessage) error {
	var cfg httpToolConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid http_tool config", agenterrors.ErrInvalidInput)
	}
	if cfg.ToolID <= 0 {
		return fmt.Errorf("%w: http_tool tool_id is required", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n HTTPToolNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Tools == nil {
		return nil, fmt.Errorf("tool repository is not configured")
	}
	var cfg httpToolConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	def, err := n.Tools.FindByID(ctx, rc.OwnerID, cfg.ToolID)
	if err != nil {
		return nil, err
	}
	if def.Status != tool.StatusActive || def.ToolType != tool.TypeHTTP {
		return nil, fmt.Errorf("%w: http tool is not active", agenterrors.ErrInvalidInput)
	}
	resolvedInput := engine.ResolveAny(cfg.Input, rc)
	inputJSON, _ := json.Marshal(resolvedInput)
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ToolStarted, RunID: rc.RunID, Payload: map[string]any{"tool_id": def.ID, "tool_name": def.Name}})
	started := time.Now()
	output, callErr := ExecuteHTTPToolDefinition(ctx, def, inputJSON)
	status := tool.InvocationStatusSucceeded
	errMessage := ""
	if callErr != nil {
		status = tool.InvocationStatusFailed
		errMessage = callErr.Error()
	}
	outputJSON, _ := json.Marshal(output)
	latencyMS := int(time.Since(started).Milliseconds())
	if n.Invocations != nil {
		_ = n.Invocations.Create(ctx, &tool.Invocation{
			OwnerID: rc.OwnerID, RunID: rc.RunID, NodeID: rc.CurrentNodeID,
			ToolID: def.ID, ToolName: def.Name, ToolType: def.ToolType,
			InputJSON: inputJSON, OutputJSON: outputJSON, Status: status,
			ErrorMessage: errMessage, LatencyMS: latencyMS,
		})
	}
	if callErr != nil {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ToolFailed, RunID: rc.RunID, Payload: map[string]any{"tool_id": def.ID, "error": errMessage, "latency_ms": latencyMS}})
		return nil, callErr
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.ToolFinished, RunID: rc.RunID, Payload: map[string]any{"tool_id": def.ID, "status_code": output["status_code"], "latency_ms": latencyMS}})
	return output, nil
}

func ExecuteHTTPToolDefinition(ctx context.Context, def *tool.Definition, inputJSON []byte) (engine.NodeOutput, error) {
	var cfg httpToolDefinitionConfig
	if err := json.Unmarshal(def.ConfigJSON, &cfg); err != nil {
		return nil, fmt.Errorf("%w: invalid http tool definition config", agenterrors.ErrInvalidInput)
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("%w: http tool only supports GET and POST", agenterrors.ErrInvalidInput)
	}
	endpoint, err := validatePublicHTTPURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	maxBytes := cfg.MaxResponseBytes
	if maxBytes <= 0 || maxBytes > 2*1024*1024 {
		maxBytes = 512 * 1024
	}
	var body io.Reader
	if method == http.MethodGet {
		var params map[string]any
		_ = json.Unmarshal(inputJSON, &params)
		query := endpoint.Query()
		for key, value := range params {
			query.Set(key, fmt.Sprint(value))
		}
		endpoint.RawQuery = query.Encode()
	} else {
		body = bytes.NewReader(inputJSON)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range cfg.Headers {
		if strings.EqualFold(key, "host") {
			continue
		}
		req.Header.Set(key, value)
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: http tool response too large", agenterrors.ErrInvalidInput)
	}
	return engine.NodeOutput{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(data),
	}, nil
}

func validatePublicHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: invalid http tool url", agenterrors.ErrInvalidInput)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: http tool url userinfo is not allowed", agenterrors.ErrInvalidInput)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: http tool only supports http/https", agenterrors.ErrInvalidInput)
	}
	host := parsed.Hostname()
	if isBlockedHost(host) {
		return nil, fmt.Errorf("%w: http tool target is not allowed", agenterrors.ErrForbidden)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("%w: http tool resolved to blocked address", agenterrors.ErrForbidden)
		}
	}
	return parsed, nil
}

func isBlockedHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSuffix(host, "."))
	if normalized == "localhost" || normalized == "metadata.google.internal" || normalized == "metadata.amazonaws.com" {
		return true
	}
	if ip := net.ParseIP(normalized); ip != nil {
		return isBlockedIP(ip)
	}
	return false
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}
