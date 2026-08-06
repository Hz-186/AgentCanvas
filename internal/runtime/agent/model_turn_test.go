package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

type streamingRunnerClient struct {
	responses []llm.ToolChatResponse
	requests  []llm.ToolChatRequest
	streamed  int
	fallback  int
}

type fallbackOnlyRunnerClient struct {
	response *llm.ToolChatResponse
	err      error
	calls    int
}

func (c *fallbackOnlyRunnerClient) ChatWithTools(context.Context, llm.ChatProviderConfig, llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.calls++
	return c.response, c.err
}

type scriptedStreamingRunnerClient struct {
	stream   func(context.Context, func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error)
	fallback *llm.ToolChatResponse
	streams  int
	calls    int
}

func (c *scriptedStreamingRunnerClient) ChatWithTools(context.Context, llm.ChatProviderConfig, llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.calls++
	return c.fallback, nil
}

func (c *scriptedStreamingRunnerClient) StreamChatWithTools(ctx context.Context, _ llm.ChatProviderConfig, _ llm.ToolChatRequest, onEvent func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
	c.streams++
	return c.stream(ctx, onEvent)
}

func (c *streamingRunnerClient) ChatWithTools(context.Context, llm.ChatProviderConfig, llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	c.fallback++
	return nil, nil
}

func (c *streamingRunnerClient) StreamChatWithTools(_ context.Context, _ llm.ChatProviderConfig, req llm.ToolChatRequest, onEvent func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
	c.streamed++
	c.requests = append(c.requests, req)
	response := c.responses[0]
	c.responses = c.responses[1:]
	if c.streamed == 1 {
		for _, event := range []llm.ModelStreamEvent{
			{Kind: llm.ModelReasoningStart},
			{Kind: llm.ModelReasoningDelta, Text: "private reasoning"},
			{Kind: llm.ModelReasoningEnd},
			{Kind: llm.ModelTextStart},
			{Kind: llm.ModelTextDelta, Text: "checking"},
			{Kind: llm.ModelTextEnd},
			{Kind: llm.ModelToolCallStart, Index: 0, CallID: "call_1", Name: "noop"},
			{Kind: llm.ModelToolCallEnd, Index: 0, CallID: "call_1", Name: "noop", Arguments: json.RawMessage(`{}`)},
			{Kind: llm.ModelDone},
		} {
			if err := onEvent(event); err != nil {
				return nil, err
			}
		}
	} else {
		for _, event := range []llm.ModelStreamEvent{{Kind: llm.ModelTextStart}, {Kind: llm.ModelTextDelta, Text: response.Message.Content}, {Kind: llm.ModelTextEnd}, {Kind: llm.ModelDone}} {
			if err := onEvent(event); err != nil {
				return nil, err
			}
		}
	}
	return &response, nil
}

func TestRunnerUsesStreamingModelTurnWithoutPersistingReasoning(t *testing.T) {
	client := &streamingRunnerClient{responses: []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "checking", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "noop", Arguments: json.RawMessage(`{}`)}}}},
		{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}},
	}}
	tool := &fakeRuntimeTool{name: "noop", output: "ok"}
	events := make([]llm.ModelStreamEvent, 0)
	runner := &Runner{LLM: client, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
		events = append(events, event)
		return nil
	}}
	result, err := runner.Run(context.Background(), RunRequest{Model: "test", Task: "run", MaxIterations: 3, MaxToolCalls: 2, Tools: []toolruntime.RuntimeTool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" || client.streamed != 2 || client.fallback != 0 {
		t.Fatalf("streaming model turn was not used: result=%+v streamed=%d fallback=%d", result, client.streamed, client.fallback)
	}
	if len(events) == 0 || events[1].Kind != llm.ModelReasoningDelta {
		t.Fatalf("reasoning event was not delivered separately: %+v", events)
	}
	for _, message := range client.requests[1].Messages {
		if strings.Contains(message.Content, "private reasoning") {
			t.Fatalf("reasoning leaked into the next model request: %+v", client.requests[1].Messages)
		}
	}
}

func TestExecuteModelTurnFallsBackWhenCachedInnerDoesNotStream(t *testing.T) {
	inner := &fallbackOnlyRunnerClient{response: &llm.ToolChatResponse{
		Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "fallback"},
		Usage:   llm.Usage{TotalTokens: 3},
	}}
	cached := llm.NewCachedChatClient(nil, inner, llm.CachedChatClientOptions{})
	var events []llm.ModelStreamEvent
	runner := &Runner{LLM: cached, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
		events = append(events, event)
		return nil
	}}

	response, err := runner.executeModelTurn(context.Background(), llm.ChatProviderConfig{}, llm.ToolChatRequest{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.Message.Content != "fallback" || inner.calls != 1 {
		t.Fatalf("non-streaming fallback was not used: response=%+v calls=%d", response, inner.calls)
	}
	want := []llm.ModelStreamKind{llm.ModelTextStart, llm.ModelTextDelta, llm.ModelTextEnd, llm.ModelUsage, llm.ModelDone}
	if got := eventKinds(events); !equalEventKinds(got, want) {
		t.Fatalf("unexpected fallback event sequence: got=%v want=%v", got, want)
	}
}

func TestExecuteModelTurnFallsBackWhenProviderRejectsStreaming(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		var payload struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Stream {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"streaming is not supported"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fallback"}}],"usage":{"total_tokens":2}}`))
	}))
	defer server.Close()

	client := &llm.OpenAICompatibleChatClient{Client: server.Client(), StreamClient: server.Client()}
	var events []llm.ModelStreamEvent
	runner := &Runner{LLM: client, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
		events = append(events, event)
		return nil
	}}
	response, err := runner.executeModelTurn(context.Background(), llm.ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL}, llm.ToolChatRequest{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || response == nil || response.Message.Content != "fallback" {
		t.Fatalf("provider fallback was not used: requests=%d response=%+v", requests, response)
	}
	for _, event := range events {
		if event.Kind == llm.ModelError {
			t.Fatalf("capability fallback must not emit a transient model error: %+v", events)
		}
	}
	if got := eventKinds(events); len(got) == 0 || got[len(got)-1] != llm.ModelDone {
		t.Fatalf("fallback must still complete the model event contract: %v", got)
	}
}

func TestExecuteModelTurnCompletesMissingTerminalEvent(t *testing.T) {
	client := &scriptedStreamingRunnerClient{stream: func(_ context.Context, emit func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
		for _, event := range []llm.ModelStreamEvent{
			{Kind: llm.ModelTextStart},
			{Kind: llm.ModelTextDelta, Text: "done"},
			{Kind: llm.ModelTextEnd},
		} {
			if err := emit(event); err != nil {
				return nil, err
			}
		}
		return &llm.ToolChatResponse{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}}, nil
	}}
	var events []llm.ModelStreamEvent
	runner := &Runner{LLM: client, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
		events = append(events, event)
		return nil
	}}

	response, err := runner.executeModelTurn(context.Background(), llm.ChatProviderConfig{}, llm.ToolChatRequest{})
	if err != nil || response == nil {
		t.Fatalf("unexpected model turn result: response=%+v err=%v", response, err)
	}
	if got := eventKinds(events); len(got) == 0 || got[len(got)-1] != llm.ModelDone {
		t.Fatalf("successful stream must end with done: %v", got)
	}
}

func TestExecuteModelTurnEmitsOneErrorWhenStreamFails(t *testing.T) {
	streamErr := errors.New("provider stream failed")
	for _, test := range []struct {
		name              string
		emitProviderError bool
	}{
		{name: "provider omitted error event"},
		{name: "provider already emitted error event", emitProviderError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedStreamingRunnerClient{stream: func(_ context.Context, emit func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
				if test.emitProviderError {
					if err := emit(llm.ModelStreamEvent{Kind: llm.ModelError, Err: streamErr}); err != nil {
						return nil, err
					}
				}
				return nil, streamErr
			}}
			var events []llm.ModelStreamEvent
			runner := &Runner{LLM: client, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
				events = append(events, event)
				return nil
			}}

			response, err := runner.executeModelTurn(context.Background(), llm.ChatProviderConfig{}, llm.ToolChatRequest{})
			if response != nil || !errors.Is(err, streamErr) {
				t.Fatalf("unexpected model turn result: response=%+v err=%v", response, err)
			}
			errorEvents := 0
			doneEvents := 0
			for _, event := range events {
				if event.Kind == llm.ModelError {
					errorEvents++
				}
				if event.Kind == llm.ModelDone {
					doneEvents++
				}
			}
			if errorEvents != 1 || doneEvents != 0 {
				t.Fatalf("stream failure must emit exactly one error event: %+v", events)
			}
		})
	}
}

func TestExecuteModelTurnSuppressesDuplicateDone(t *testing.T) {
	client := &scriptedStreamingRunnerClient{stream: func(_ context.Context, emit func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
		if err := emit(llm.ModelStreamEvent{Kind: llm.ModelDone}); err != nil {
			return nil, err
		}
		if err := emit(llm.ModelStreamEvent{Kind: llm.ModelDone}); err != nil {
			return nil, err
		}
		return &llm.ToolChatResponse{Message: llm.ChatMessage{Role: conversation.RoleAssistant, Content: "done"}}, nil
	}}
	doneEvents := 0
	runner := &Runner{LLM: client, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
		if event.Kind == llm.ModelDone {
			doneEvents++
		}
		return nil
	}}
	if _, err := runner.executeModelTurn(context.Background(), llm.ChatProviderConfig{}, llm.ToolChatRequest{}); err != nil {
		t.Fatal(err)
	}
	if doneEvents != 1 {
		t.Fatalf("one model turn must publish exactly one done event, got %d", doneEvents)
	}
}

func TestExecuteModelTurnRejectsNilSuccessfulResponse(t *testing.T) {
	client := &scriptedStreamingRunnerClient{stream: func(context.Context, func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
		return nil, nil
	}}
	var events []llm.ModelStreamEvent
	runner := &Runner{LLM: client, OnModelEvent: func(_ context.Context, event llm.ModelStreamEvent) error {
		events = append(events, event)
		return nil
	}}

	response, err := runner.executeModelTurn(context.Background(), llm.ChatProviderConfig{}, llm.ToolChatRequest{})
	if response != nil || err == nil {
		t.Fatalf("nil successful response must be rejected: response=%+v err=%v", response, err)
	}
	if got := eventKinds(events); len(got) != 1 || got[0] != llm.ModelError {
		t.Fatalf("nil response must produce one error event: %v", got)
	}
}

func TestRunnerTreatsModelStreamDeadlineAsTimeout(t *testing.T) {
	client := &scriptedStreamingRunnerClient{stream: func(ctx context.Context, _ func(llm.ModelStreamEvent) error) (*llm.ToolChatResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	runner := NewRunner(client)
	result, err := runner.Run(context.Background(), RunRequest{
		Model:              "test",
		Task:               "wait",
		MaxIterations:      1,
		MaxToolCalls:       1,
		MaxExecutionTimeMS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonTimeout {
		t.Fatalf("deadline must stop the run as timeout: %+v", result)
	}
}

func eventKinds(events []llm.ModelStreamEvent) []llm.ModelStreamKind {
	kinds := make([]llm.ModelStreamKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func equalEventKinds(left, right []llm.ModelStreamKind) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
