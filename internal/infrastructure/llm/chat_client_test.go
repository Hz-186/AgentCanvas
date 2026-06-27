package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization: %s", got)
		}
		var req openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "test-model" || len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
			t.Fatalf("unexpected request: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(openAIChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{{Message: ChatMessage{Role: "assistant", Content: "world"}}},
			Usage: Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		})
	}))
	defer server.Close()

	client := &OpenAICompatibleChatClient{Client: server.Client()}
	resp, err := client.Chat(context.Background(), ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL, APIKey: "test-key"}, ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "world" || resp.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestOpenAICompatibleStreamChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if !req.Stream || !req.StreamOptions["include_usage"] {
			t.Fatalf("stream options not enabled: %+v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := &OpenAICompatibleChatClient{Client: server.Client()}
	content := ""
	usage := Usage{}
	done := false
	err := client.StreamChat(context.Background(), ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL}, ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}, func(event StreamEvent) error {
		content += event.Delta
		if event.Usage.TotalTokens > 0 {
			usage = event.Usage
		}
		if event.Done {
			done = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if content != "hello" || usage.TotalTokens != 3 || !done {
		t.Fatalf("unexpected stream: content=%q usage=%+v done=%v", content, usage, done)
	}
}

func TestOpenAICompatibleChatWithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openAIToolChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "search_knowledge" {
			t.Fatalf("unexpected tools: %+v", req.Tools)
		}
		if req.ToolChoice != "auto" {
			t.Fatalf("unexpected tool choice: %+v", req.ToolChoice)
		}
		_ = json.NewEncoder(w).Encode(openAIToolChatResponse{
			Choices: []struct {
				Message openAIToolChatMessage `json:"message"`
			}{{
				Message: openAIToolChatMessage{
					Role: "assistant",
					ToolCalls: []openAIToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: openAIToolFunction{
							Name:      "search_knowledge",
							Arguments: `{"query":"agent"}`,
						},
					}},
				},
			}},
			Usage: Usage{TotalTokens: 9},
		})
	}))
	defer server.Close()

	client := &OpenAICompatibleChatClient{Client: server.Client()}
	resp, err := client.ChatWithTools(context.Background(), ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL}, ToolChatRequest{
		Model:      "test-model",
		Messages:   []ChatMessage{{Role: "user", Content: "hello"}},
		ToolChoice: "auto",
		Tools: []ToolDefinition{{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:       "search_knowledge",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.TotalTokens != 9 || len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("unexpected tool response: %+v", resp)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "search_knowledge" || string(call.Arguments) != `{"query":"agent"}` {
		t.Fatalf("unexpected call: %+v", call)
	}
}
