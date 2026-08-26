package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ChatProviderConfig struct {
	ProviderType string
	BaseURL      string
	APIKey       string
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
}

type Usage struct {
	PromptTokens      int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	TotalTokens       int `json:"total_tokens"`
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int `json:"reasoning_tokens,omitempty"`
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	var value struct {
		PromptTokens      int `json:"prompt_tokens"`
		CompletionTokens  int `json:"completion_tokens"`
		TotalTokens       int `json:"total_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		ReasoningTokens   int `json:"reasoning_tokens"`
		PromptDetails     struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*u = Usage{PromptTokens: value.PromptTokens, CompletionTokens: value.CompletionTokens, TotalTokens: value.TotalTokens, CachedInputTokens: value.CachedInputTokens, ReasoningTokens: value.ReasoningTokens}
	if u.CachedInputTokens == 0 {
		u.CachedInputTokens = value.PromptDetails.CachedTokens
	}
	if u.ReasoningTokens == 0 {
		u.ReasoningTokens = value.CompletionDetails.ReasoningTokens
	}
	return nil
}

type ChatResponse struct {
	Content string `json:"content"`
	Usage   Usage  `json:"usage"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Type      string          `json:"type,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolChatRequest struct {
	Model           string           `json:"model"`
	Messages        []ChatMessage    `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      any              `json:"tool_choice,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

type ToolChatResponse struct {
	Message      ChatMessage `json:"message"`
	Usage        Usage       `json:"usage"`
	ProposedPlan string      `json:"proposed_plan,omitempty"`
}

type StreamEvent struct {
	Delta string
	Usage Usage
	Done  bool
}

type ChatClient interface {
	Chat(ctx context.Context, cfg ChatProviderConfig, req ChatRequest) (*ChatResponse, error)
	StreamChat(ctx context.Context, cfg ChatProviderConfig, req ChatRequest, onEvent func(StreamEvent) error) error
}

type ToolCallingClient interface {
	ChatWithTools(ctx context.Context, cfg ChatProviderConfig, req ToolChatRequest) (*ToolChatResponse, error)
}

type OpenAICompatibleChatClient struct {
	Client       *http.Client
	StreamClient *http.Client
}

func NewOpenAICompatibleChatClient() *OpenAICompatibleChatClient {
	return &OpenAICompatibleChatClient{
		Client:       &http.Client{Timeout: 60 * time.Second},
		StreamClient: &http.Client{Timeout: 0},
	}
}

func (c *OpenAICompatibleChatClient) Chat(ctx context.Context, cfg ChatProviderConfig, req ChatRequest) (*ChatResponse, error) {
	endpoint, err := openAIChatEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	payload := openAIChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
	}
	var resp openAIChatResponse
	if err := c.doJSON(ctx, endpoint, cfg.APIKey, payload, &resp); err != nil {
		return nil, err
	}
	content := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
	}
	return &ChatResponse{Content: content, Usage: resp.Usage}, nil
}

func (c *OpenAICompatibleChatClient) ChatWithTools(ctx context.Context, cfg ChatProviderConfig, req ToolChatRequest) (*ToolChatResponse, error) {
	endpoint, err := openAIChatEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	payload := openAIToolChatRequest{
		Model:           req.Model,
		Messages:        toOpenAIToolMessages(req.Messages),
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		Temperature:     req.Temperature,
		ReasoningEffort: req.ReasoningEffort,
	}
	var resp openAIToolChatResponse
	if err := c.doJSON(ctx, endpoint, cfg.APIKey, payload, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return &ToolChatResponse{}, nil
	}
	return &ToolChatResponse{
		Message: fromOpenAIToolMessage(resp.Choices[0].Message),
		Usage:   resp.Usage,
	}, nil
}

func (c *OpenAICompatibleChatClient) StreamChat(ctx context.Context, cfg ChatProviderConfig, req ChatRequest, onEvent func(StreamEvent) error) error {
	endpoint, err := openAIChatEndpoint(cfg)
	if err != nil {
		return err
	}
	payload := openAIChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		Stream:      true,
		StreamOptions: map[string]bool{
			"include_usage": true,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setOpenAIHeaders(httpReq, cfg.APIKey)
	resp, err := c.streamClient().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return classifyHTTPError(resp.StatusCode, resp.Status, data, "chat stream failed")
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return onEvent(StreamEvent{Done: true})
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			if err := onEvent(StreamEvent{Usage: chunk.Usage}); err != nil {
				return err
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			if err := onEvent(StreamEvent{Delta: choice.Delta.Content}); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return onEvent(StreamEvent{Done: true})
}

// StreamChatWithTools streams an OpenAI-compatible tool-calling response and
// translates provider fields into ModelStreamEvent values.  Tool arguments
// are deliberately kept as raw fragments until the provider stream finishes;
// this allows callers to observe deltas without ever receiving partial JSON
// as a completed ToolCall.
func (c *OpenAICompatibleChatClient) StreamChatWithTools(ctx context.Context, cfg ChatProviderConfig, req ToolChatRequest, onEvent func(ModelStreamEvent) error) (*ToolChatResponse, error) {
	if onEvent == nil {
		return nil, fmt.Errorf("stream event callback is required")
	}
	endpoint, err := openAIChatEndpoint(cfg)
	if err != nil {
		return nil, emitModelStreamError(onEvent, err)
	}
	payload := openAIToolChatRequest{
		Model:           req.Model,
		Messages:        toOpenAIToolMessages(req.Messages),
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		Temperature:     req.Temperature,
		ReasoningEffort: req.ReasoningEffort,
		Stream:          true,
		StreamOptions: map[string]bool{
			"include_usage": true,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, emitModelStreamError(onEvent, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, emitModelStreamError(onEvent, err)
	}
	setOpenAIHeaders(httpReq, cfg.APIKey)
	resp, err := c.streamClient().Do(httpReq)
	if err != nil {
		if errors.Is(err, ErrToolStreamingUnsupported) {
			return nil, err
		}
		return nil, emitModelStreamError(onEvent, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		streamErr := classifyHTTPError(resp.StatusCode, resp.Status, data, "chat tool stream failed")
		if isToolStreamingUnsupportedResponse(resp.StatusCode, data) {
			return nil, fmt.Errorf("%w: %v", ErrToolStreamingUnsupported, streamErr)
		}
		return nil, emitModelStreamError(onEvent, streamErr)
	}

	parser := newToolStreamParser(NewToolStreamAccumulator(), onEvent)
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 4*1024*1024)
	sawDone := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			sawDone = true
			if err := parser.finish(); err != nil {
				return nil, err
			}
			break
		}
		var chunk openAIToolStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, parser.fail(err)
		}
		if err := parser.accept(chunk); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
		return nil, parser.fail(err)
	}
	if !sawDone {
		if err := ctx.Err(); err != nil {
			return nil, parser.fail(err)
		}
		return nil, parser.fail(io.ErrUnexpectedEOF)
	}
	return parser.acc.Response()
}

func isToolStreamingUnsupportedResponse(statusCode int, body []byte) bool {
	if statusCode == http.StatusNotImplemented {
		return true
	}
	if statusCode < http.StatusBadRequest || statusCode >= http.StatusInternalServerError {
		return false
	}
	message := strings.ToLower(string(body))
	if !strings.Contains(message, "stream") {
		return false
	}
	return strings.Contains(message, "not supported") ||
		strings.Contains(message, "unsupported") ||
		strings.Contains(message, "not implemented")
}

func (c *OpenAICompatibleChatClient) doJSON(ctx context.Context, endpoint, apiKey string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setOpenAIHeaders(req, apiKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return classifyHTTPError(resp.StatusCode, resp.Status, data, "chat failed")
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func classifyHTTPError(statusCode int, status string, body []byte, prefix string) error {
	err := fmt.Errorf("%s: %s: %s", prefix, status, strings.TrimSpace(string(body)))
	message := strings.ToLower(string(body))
	contextMessage := strings.Contains(message, "context_length_exceeded") || strings.Contains(message, "maximum context length") || strings.Contains(message, "too many tokens")
	if statusCode == http.StatusRequestEntityTooLarge || statusCode == http.StatusBadRequest && contextMessage {
		return fmt.Errorf("%w: %v", ErrContextWindowExceeded, err)
	}
	if statusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %v", ErrRateLimited, err)
	}
	return err
}

func (c *OpenAICompatibleChatClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

func (c *OpenAICompatibleChatClient) streamClient() *http.Client {
	if c.StreamClient != nil {
		return c.StreamClient
	}
	return http.DefaultClient
}

func openAIChatEndpoint(cfg ChatProviderConfig) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	switch cfg.ProviderType {
	case "deepseek":
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
	case "qwen":
		if baseURL == "" {
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
	case "openai_compatible", "azure_openai":
		if baseURL == "" {
			return "", fmt.Errorf("base_url is required for %s", cfg.ProviderType)
		}
	case "ollama", "local":
		return "", fmt.Errorf("provider type %s does not support chat completions yet", cfg.ProviderType)
	default:
		return "", fmt.Errorf("unsupported provider type: %s", cfg.ProviderType)
	}
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL, nil
	}
	if strings.HasSuffix(baseURL, "/v1") || strings.Contains(baseURL, "/compatible-mode/v1") {
		return baseURL + "/chat/completions", nil
	}
	return baseURL + "/v1/chat/completions", nil
}

func setOpenAIHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

type openAIChatRequest struct {
	Model         string          `json:"model"`
	Messages      []ChatMessage   `json:"messages"`
	Temperature   *float64        `json:"temperature,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta ChatMessage `json:"delta"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type openAIToolChatRequest struct {
	Model           string                  `json:"model"`
	Messages        []openAIToolChatMessage `json:"messages"`
	Tools           []ToolDefinition        `json:"tools,omitempty"`
	ToolChoice      any                     `json:"tool_choice,omitempty"`
	Temperature     *float64                `json:"temperature,omitempty"`
	ReasoningEffort string                  `json:"reasoning_effort,omitempty"`
	Stream          bool                    `json:"stream,omitempty"`
	StreamOptions   map[string]bool         `json:"stream_options,omitempty"`
}

type openAIToolChatResponse struct {
	Choices []struct {
		Message openAIToolChatMessage `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type openAIToolChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIToolStreamChunk keeps the stream-only representation separate from
// the non-streaming response types above.  Usage is a pointer so a usage-only
// chunk with all zero values can still be distinguished from a chunk that does
// not carry usage at all.
type openAIToolStreamChunk struct {
	Choices []struct {
		Index        int                   `json:"index,omitempty"`
		FinishReason string                `json:"finish_reason,omitempty"`
		Delta        openAIToolStreamDelta `json:"delta"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

type openAIToolStreamDelta struct {
	Content          string                 `json:"content"`
	ReasoningContent string                 `json:"reasoning_content"`
	Reasoning        string                 `json:"reasoning"`
	ToolCalls        []openAIToolStreamCall `json:"tool_calls"`
}

type openAIToolStreamCall struct {
	Index    *int                     `json:"index"`
	ID       string                   `json:"id"`
	Type     string                   `json:"type"`
	Function openAIToolStreamFunction `json:"function"`
}

type openAIToolStreamFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolStreamParser struct {
	acc           *ToolStreamAccumulator
	onEvent       func(ModelStreamEvent) error
	textOpen      bool
	reasoningOpen bool
	toolStarted   map[int]bool
	toolEnded     map[int]bool
	failed        bool
	done          bool
}

func newToolStreamParser(acc *ToolStreamAccumulator, onEvent func(ModelStreamEvent) error) *toolStreamParser {
	return &toolStreamParser{
		acc:         acc,
		onEvent:     onEvent,
		toolStarted: make(map[int]bool),
		toolEnded:   make(map[int]bool),
	}
}

func (p *toolStreamParser) accept(chunk openAIToolStreamChunk) error {
	for _, choice := range chunk.Choices {
		reasoning := choice.Delta.ReasoningContent
		if reasoning == "" {
			reasoning = choice.Delta.Reasoning
		}
		if reasoning != "" {
			if err := p.beginReasoning(); err != nil {
				return err
			}
			if err := p.emit(ModelStreamEvent{Kind: ModelReasoningDelta, Text: reasoning}); err != nil {
				return err
			}
		}
		if content := choice.Delta.Content; content != "" {
			if err := p.beginText(); err != nil {
				return err
			}
			p.acc.AddText(content)
			if err := p.emit(ModelStreamEvent{Kind: ModelTextDelta, Text: content}); err != nil {
				return err
			}
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			index := 0
			if toolCall.Index != nil {
				index = *toolCall.Index
			}
			if index < 0 {
				index = 0
			}
			first := !p.toolStarted[index]
			if first {
				if err := p.closeTextAndReasoning(); err != nil {
					return err
				}
				p.toolStarted[index] = true
			}
			p.acc.AddToolCallDelta(index, toolCall.ID, toolCall.Function.Name, toolCall.Function.Arguments)
			if first {
				if err := p.emit(ModelStreamEvent{
					Kind:   ModelToolCallStart,
					Index:  index,
					CallID: toolCall.ID,
					Name:   toolCall.Function.Name,
				}); err != nil {
					return err
				}
			}
			if toolCall.Function.Arguments != "" || (!first && (toolCall.ID != "" || toolCall.Function.Name != "")) {
				if err := p.emit(ModelStreamEvent{
					Kind:          ModelToolCallDelta,
					Index:         index,
					CallID:        toolCall.ID,
					Name:          toolCall.Function.Name,
					ArgumentDelta: toolCall.Function.Arguments,
				}); err != nil {
					return err
				}
			}
		}
		if choice.FinishReason != "" {
			if err := p.finishForReason(choice.FinishReason); err != nil {
				return err
			}
		}
	}
	if chunk.Usage != nil {
		// Usage-only chunks are sent after the final choice by most providers.
		// Close any still-open segments before publishing usage so consumers see
		// a complete text/tool lifecycle before the terminal accounting event.
		if err := p.closeTextAndReasoning(); err != nil {
			return err
		}
		if err := p.finishTools(); err != nil {
			return err
		}
		p.acc.AddUsage(*chunk.Usage)
		if err := p.emit(ModelStreamEvent{Kind: ModelUsage, Usage: *chunk.Usage}); err != nil {
			return err
		}
	}
	return nil
}

func (p *toolStreamParser) finishForReason(reason string) error {
	switch reason {
	case "tool_calls", "function_call", "stop", "length", "content_filter":
		if err := p.closeTextAndReasoning(); err != nil {
			return err
		}
		return p.finishTools()
	default:
		return nil
	}
}

func (p *toolStreamParser) beginText() error {
	if p.textOpen {
		return nil
	}
	if p.reasoningOpen {
		if err := p.emit(ModelStreamEvent{Kind: ModelReasoningEnd}); err != nil {
			return err
		}
		p.reasoningOpen = false
	}
	p.textOpen = true
	return p.emit(ModelStreamEvent{Kind: ModelTextStart})
}

func (p *toolStreamParser) beginReasoning() error {
	if p.reasoningOpen {
		return nil
	}
	if p.textOpen {
		if err := p.emit(ModelStreamEvent{Kind: ModelTextEnd}); err != nil {
			return err
		}
		p.textOpen = false
	}
	p.reasoningOpen = true
	return p.emit(ModelStreamEvent{Kind: ModelReasoningStart})
}

func (p *toolStreamParser) closeTextAndReasoning() error {
	if p.textOpen {
		if err := p.emit(ModelStreamEvent{Kind: ModelTextEnd}); err != nil {
			return err
		}
		p.textOpen = false
	}
	if p.reasoningOpen {
		if err := p.emit(ModelStreamEvent{Kind: ModelReasoningEnd}); err != nil {
			return err
		}
		p.reasoningOpen = false
	}
	return nil
}

func (p *toolStreamParser) finish() error {
	if p.done {
		return nil
	}
	if err := p.closeTextAndReasoning(); err != nil {
		return err
	}
	if err := p.finishTools(); err != nil {
		return err
	}
	p.done = true
	return p.emit(ModelStreamEvent{Kind: ModelDone})
}

func (p *toolStreamParser) finishTools() error {
	for _, index := range p.acc.orderedToolIndexes() {
		if p.toolEnded[index] {
			continue
		}
		call, err := p.acc.ToolCall(index)
		if err != nil {
			return p.fail(err)
		}
		if err := p.emit(ModelStreamEvent{
			Kind:      ModelToolCallEnd,
			Index:     index,
			CallID:    call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		}); err != nil {
			return err
		}
		p.toolEnded[index] = true
	}
	return nil
}

func (p *toolStreamParser) emit(event ModelStreamEvent) error {
	if p.onEvent == nil {
		return fmt.Errorf("stream event callback is required")
	}
	return p.onEvent(event)
}

func (p *toolStreamParser) fail(err error) error {
	if err == nil {
		err = fmt.Errorf("tool stream failed")
	}
	if p.failed {
		return err
	}
	p.failed = true
	if callbackErr := p.emit(ModelStreamEvent{Kind: ModelError, Err: err}); callbackErr != nil {
		return callbackErr
	}
	return err
}

func emitModelStreamError(onEvent func(ModelStreamEvent) error, err error) error {
	if callbackErr := onEvent(ModelStreamEvent{Kind: ModelError, Err: err}); callbackErr != nil {
		return callbackErr
	}
	return err
}

func toOpenAIToolMessages(messages []ChatMessage) []openAIToolChatMessage {
	out := make([]openAIToolChatMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, openAIToolChatMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			ToolCalls:  toOpenAIToolCalls(msg.ToolCalls),
		})
	}
	return out
}

func toOpenAIToolCalls(calls []ToolCall) []openAIToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		typ := call.Type
		if typ == "" {
			typ = "function"
		}
		out = append(out, openAIToolCall{
			ID:   call.ID,
			Type: typ,
			Function: openAIToolFunction{
				Name:      call.Name,
				Arguments: rawJSONToString(call.Arguments),
			},
		})
	}
	return out
}

func fromOpenAIToolMessage(msg openAIToolChatMessage) ChatMessage {
	return ChatMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
		ToolCalls:  fromOpenAIToolCalls(msg.ToolCalls),
	}
}

func fromOpenAIToolCalls(calls []openAIToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		args := json.RawMessage(strings.TrimSpace(call.Function.Arguments))
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		out = append(out, ToolCall{
			ID:        call.ID,
			Type:      call.Type,
			Name:      call.Function.Name,
			Arguments: args,
		})
	}
	return out
}

func rawJSONToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
