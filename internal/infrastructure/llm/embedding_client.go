package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type EmbeddingProviderConfig struct {
	ProviderType string
	BaseURL      string
	APIKey       string
}

type EmbeddingRequest struct {
	Model string
	Input []string
}

type EmbeddingResponse struct {
	Embeddings [][]float32
	Usage      Usage
}

type EmbeddingClient interface {
	Embed(ctx context.Context, cfg EmbeddingProviderConfig, req EmbeddingRequest) (*EmbeddingResponse, error)
}

type OpenAICompatibleEmbeddingClient struct {
	Client *http.Client
}

func NewOpenAICompatibleEmbeddingClient() *OpenAICompatibleEmbeddingClient {
	return &OpenAICompatibleEmbeddingClient{Client: &http.Client{Timeout: 60 * time.Second}}
}

func (c *OpenAICompatibleEmbeddingClient) Embed(ctx context.Context, cfg EmbeddingProviderConfig, req EmbeddingRequest) (*EmbeddingResponse, error) {
	endpoint, err := openAIEmbeddingsEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Model) == "" || len(req.Input) == 0 {
		return nil, fmt.Errorf("embedding model and input are required")
	}
	payload := openAIEmbeddingRequest{Model: req.Model, Input: req.Input}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setOpenAIHeaders(httpReq, cfg.APIKey)
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embedding failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var parsed openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	embeddings := make([][]float32, len(parsed.Data))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(parsed.Data) {
			return nil, fmt.Errorf("embedding response index out of range: %d", item.Index)
		}
		embeddings[item.Index] = item.Embedding
	}
	return &EmbeddingResponse{Embeddings: embeddings, Usage: parsed.Usage}, nil
}

func (c *OpenAICompatibleEmbeddingClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

func openAIEmbeddingsEndpoint(cfg EmbeddingProviderConfig) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	switch cfg.ProviderType {
	case "qwen":
		if baseURL == "" {
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
	case "openai_compatible", "azure_openai":
		if baseURL == "" {
			return "", fmt.Errorf("base_url is required for %s", cfg.ProviderType)
		}
	case "deepseek", "ollama", "local":
		return "", fmt.Errorf("provider type %s does not support openai-compatible embeddings", cfg.ProviderType)
	default:
		return "", fmt.Errorf("unsupported provider type: %s", cfg.ProviderType)
	}
	if strings.HasSuffix(baseURL, "/embeddings") {
		return baseURL, nil
	}
	if strings.HasSuffix(baseURL, "/v1") || strings.Contains(baseURL, "/compatible-mode/v1") {
		return baseURL + "/embeddings", nil
	}
	return baseURL + "/v1/embeddings", nil
}

type openAIEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage Usage `json:"usage"`
}
