package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ProviderTestConfig struct {
	ProviderType string
	BaseURL      string
	APIKey       string
}

type ProviderTester interface {
	Test(ctx context.Context, cfg ProviderTestConfig) error
}

type HTTPProviderTester struct {
	Client *http.Client
}

func NewHTTPProviderTester() *HTTPProviderTester {
	return &HTTPProviderTester{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *HTTPProviderTester) Test(ctx context.Context, cfg ProviderTestConfig) error {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	switch cfg.ProviderType {
	case "local":
		return nil
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return t.get(ctx, baseURL+"/api/tags", "")
	case "deepseek":
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		return t.get(ctx, openAIModelsEndpoint(baseURL), cfg.APIKey)
	case "qwen":
		if baseURL == "" {
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
		return t.get(ctx, openAIModelsEndpoint(baseURL), cfg.APIKey)
	case "openai_compatible", "azure_openai":
		if baseURL == "" {
			return fmt.Errorf("base_url is required for %s", cfg.ProviderType)
		}
		return t.get(ctx, openAIModelsEndpoint(baseURL), cfg.APIKey)
	default:
		return fmt.Errorf("unsupported provider type: %s", cfg.ProviderType)
	}
}

func openAIModelsEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") || strings.Contains(baseURL, "/compatible-mode/v1") {
		return baseURL + "/models"
	}
	return baseURL + "/v1/models"
}

func (t *HTTPProviderTester) get(ctx context.Context, endpoint, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider test failed: %s", resp.Status)
	}
	return nil
}
