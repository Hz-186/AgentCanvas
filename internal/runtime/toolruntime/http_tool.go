package toolruntime

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

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/tool"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type HTTPRuntimeTool struct {
	def         *tool.Definition
	invocations tool.InvocationRepository
	name        string
}

func NewHTTPRuntimeTool(def *tool.Definition, invocations tool.InvocationRepository) HTTPRuntimeTool {
	return HTTPRuntimeTool{
		def:         def,
		invocations: invocations,
		name:        normalizeToolName(def.Name, def.ID),
	}
}

func (t HTTPRuntimeTool) Name() string { return t.name }

func (t HTTPRuntimeTool) Description() string {
	description := strings.TrimSpace(t.def.Description)
	if description == "" {
		description = "Call the configured HTTP API and return its response."
	}
	return description
}

func (t HTTPRuntimeTool) Parameters() json.RawMessage {
	return defaultObjectSchema(t.def.InputSchemaJSON)
}

func (t HTTPRuntimeTool) Metadata() ToolMetadata {
	metadata := ToolMetadata{RiskLevel: RiskHigh, SideEffect: SideEffectExternalAction, TimeoutMS: 5000, MaxOutputBytes: 512 * 1024}
	var cfg httpDefinitionConfig
	if err := json.Unmarshal(t.def.ConfigJSON, &cfg); err == nil {
		if cfg.TimeoutMS > 0 {
			metadata.TimeoutMS = cfg.TimeoutMS
		}
		if cfg.MaxResponseBytes > 0 {
			metadata.MaxOutputBytes = int(cfg.MaxResponseBytes)
		}
		if endpoint, err := url.Parse(strings.TrimSpace(cfg.URL)); err == nil && endpoint.Hostname() != "" {
			metadata.AllowedHosts = []string{endpoint.Hostname()}
		}
	}
	return metadata
}

func (t HTTPRuntimeTool) Execute(
	ctx context.Context, rc ToolRunContext, input json.RawMessage,
) (*ToolResult, error) {
	started := time.Now()
	output, callErr := ExecuteHTTPDefinition(ctx, t.def, input)
	status := tool.InvocationStatusSucceeded
	errMessage := ""
	if callErr != nil {
		status = tool.InvocationStatusFailed
		errMessage = callErr.Error()
	}
	outputJSON, _ := json.Marshal(output)
	if t.invocations != nil {
		_ = t.invocations.Create(ctx, &tool.Invocation{
			ImmutableModel: domain.ImmutableModel{OwnerID: rc.OwnerID},
			RunID:          rc.RunID,
			AgentID:        rc.AgentID,
			ToolID:         t.def.ID,
			ToolName:       t.Name(),
			ToolType:       t.def.ToolType,
			InputJSON:      input,
			OutputJSON:     outputJSON,
			Status:         status,
			ErrorMessage:   errMessage,
			LatencyMS:      int(time.Since(started).Milliseconds()),
		})
	}
	if callErr != nil {
		return &ToolResult{
			ContentText: errMessage,
			IsError:     true,
		}, callErr
	}
	return &ToolResult{
		ContentJSON: outputJSON,
		ContentText: string(outputJSON),
		Metadata: map[string]any{
			"tool_id": t.def.ID,
		},
	}, nil
}

type httpDefinitionConfig struct {
	URL              string            `json:"url"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	TimeoutMS        int               `json:"timeout_ms"`
	MaxResponseBytes int64             `json:"max_response_bytes"`
}

func ExecuteHTTPDefinition(ctx context.Context, def *tool.Definition, inputJSON []byte) (map[string]any, error) {
	var cfg httpDefinitionConfig
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
	return map[string]any{
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
