package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Content string `json:"content"`
	Usage   Usage  `json:"usage"`
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
		return fmt.Errorf("chat stream failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
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
		return fmt.Errorf("chat failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
